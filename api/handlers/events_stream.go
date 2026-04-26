package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"reconcileos.dev/api/db"

	"github.com/MicahParks/keyfunc/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	postgrest "github.com/supabase-community/postgrest-go"
)

const (
	maxSSETokenLength      = 4096
	maxSSEOrgIDLength      = 128
	maxSSEUserIDLength     = 128
	ssePollInterval        = 3 * time.Second
	sseHeartbeatInterval   = 15 * time.Second
	sseRecentWindowSeconds = 10
)

type eventsStreamHandler struct {
	clients       *db.SupabaseClients
	supabaseURL   string
	supabaseAnon  string
	jwksURL       string
	jwks          *keyfunc.JWKS
	jwksInitError error
	jwksMu        sync.RWMutex
}

type executionStreamRow struct {
	ID             string     `json:"id"`
	OrgID          string     `json:"org_id"`
	BotID          string     `json:"bot_id"`
	RepoID         string     `json:"repo_id"`
	Status         string     `json:"status"`
	RequiresReview bool       `json:"requires_review"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
}

type reviewQueueStreamRow struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id"`
	ExecutionID   string     `json:"execution_id"`
	Status        string     `json:"status"`
	ReviewedAt    *time.Time `json:"reviewed_at"`
	CreatedAt     time.Time  `json:"created_at"`
	DiffContent   string     `json:"diff_content"`
	ClaudeSummary string     `json:"claude_summary"`
}

type attestationStreamRow struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	ExecutionID   string    `json:"execution_id"`
	ArtifactHash  string    `json:"artifact_hash"`
	RekorLogIndex int64     `json:"rekor_log_index"`
	RekorUUID     string    `json:"rekor_uuid"`
	SignedAt      time.Time `json:"signed_at"`
}

func EventsStream(clients *db.SupabaseClients, supabaseURL, supabaseAnonKey string) gin.HandlerFunc {
	handler := &eventsStreamHandler{
		clients:      clients,
		supabaseURL:  strings.TrimSpace(supabaseURL),
		supabaseAnon: strings.TrimSpace(supabaseAnonKey),
	}
	return handler.handleStream()
}

func (h *eventsStreamHandler) handleStream() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.clients == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, reviewQueueErrorResponse{Error: "database client not configured"})
			return
		}
		token := strings.TrimSpace(c.Query("token"))
		if token == "" || len(token) > maxSSETokenLength {
			c.AbortWithStatusJSON(http.StatusUnauthorized, reviewQueueErrorResponse{Error: "missing or invalid stream token"})
			return
		}

		userID, orgID, err := h.authenticateToken(c.Request.Context(), token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, reviewQueueErrorResponse{Error: "invalid stream token"})
			return
		}

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, reviewQueueErrorResponse{Error: "streaming unsupported"})
			return
		}

		_ = userID
		ctx := c.Request.Context()
		heartbeatTicker := time.NewTicker(sseHeartbeatInterval)
		pollTicker := time.NewTicker(ssePollInterval)
		defer heartbeatTicker.Stop()
		defer pollTicker.Stop()

		sentExecutions := map[string]struct{}{}
		sentQueueItems := map[string]struct{}{}
		sentAttestations := map[string]struct{}{}

		_ = writeHeartbeat(c.Writer, flusher)
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeatTicker.C:
				if err := writeHeartbeat(c.Writer, flusher); err != nil {
					return
				}
			case <-pollTicker.C:
				since := time.Now().UTC().Add(-sseRecentWindowSeconds * time.Second)
				executions, reviews, attestations, pollErr := h.pollRecentUpdates(ctx, orgID, since)
				if pollErr != nil {
					if writeSSEEvent(c.Writer, flusher, "error", map[string]string{"error": "stream polling failed"}) != nil {
						return
					}
					continue
				}
				for _, execution := range executions {
					eventKey := strings.TrimSpace(execution.ID)
					if _, exists := sentExecutions[eventKey]; exists {
						continue
					}
					if writeSSEEvent(c.Writer, flusher, "execution_update", execution) != nil {
						return
					}
					sentExecutions[eventKey] = struct{}{}
				}
				for _, review := range reviews {
					eventKey := strings.TrimSpace(review.ID)
					if _, exists := sentQueueItems[eventKey]; exists {
						continue
					}
					if writeSSEEvent(c.Writer, flusher, "review_required", review) != nil {
						return
					}
					sentQueueItems[eventKey] = struct{}{}
				}
				for _, attestation := range attestations {
					eventKey := strings.TrimSpace(attestation.ID)
					if _, exists := sentAttestations[eventKey]; exists {
						continue
					}
					if writeSSEEvent(c.Writer, flusher, "attestation_issued", attestation) != nil {
						return
					}
					sentAttestations[eventKey] = struct{}{}
				}
			}
		}
	}
}

func (h *eventsStreamHandler) authenticateToken(ctx context.Context, token string) (string, string, error) {
	if h.supabaseURL == "" || h.supabaseAnon == "" {
		return "", "", fmt.Errorf("supabase auth is not configured")
	}
	parsedURL, err := url.Parse(h.supabaseURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return "", "", fmt.Errorf("supabase URL is invalid")
	}

	jwksURL := strings.TrimRight(h.supabaseURL, "/") + "/auth/v1/keys"
	trimmedToken := strings.TrimSpace(token)
	if trimmedToken == "" || len(trimmedToken) > maxSSETokenLength {
		return "", "", fmt.Errorf("token is invalid")
	}
	resolvedJWKS, err := h.getOrInitJWKS(ctx, jwksURL)
	if err != nil {
		return "", "", err
	}

	claims := jwt.RegisteredClaims{}
	parsedToken, err := jwt.ParseWithClaims(trimmedToken, &claims, resolvedJWKS.Keyfunc)
	if err != nil || parsedToken == nil || !parsedToken.Valid {
		return "", "", fmt.Errorf("token validation failed")
	}
	userID := strings.TrimSpace(claims.Subject)
	if userID == "" || len(userID) > maxSSEUserIDLength {
		return "", "", fmt.Errorf("token subject is invalid")
	}
	if _, err := uuid.Parse(userID); err != nil {
		return "", "", fmt.Errorf("token subject is invalid")
	}

	orgID, err := h.lookupOrgIDByUserID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	return userID, orgID, nil
}

func (h *eventsStreamHandler) getOrInitJWKS(ctx context.Context, jwksURL string) (*keyfunc.JWKS, error) {
	h.jwksMu.RLock()
	if h.jwks != nil {
		defer h.jwksMu.RUnlock()
		return h.jwks, nil
	}
	h.jwksMu.RUnlock()

	h.jwksMu.Lock()
	defer h.jwksMu.Unlock()
	if h.jwks != nil {
		return h.jwks, nil
	}

	jwks, err := keyfunc.Get(jwksURL, keyfunc.Options{
		RefreshInterval:   time.Hour,
		RefreshUnknownKID: true,
		Client:            &http.Client{Timeout: 10 * time.Second},
		RequestFactory: func(ctx context.Context, reqURL string) (*http.Request, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			if err != nil {
				return nil, err
			}
			request.Header.Set("apikey", h.supabaseAnon)
			request.Header.Set("Authorization", "Bearer "+h.supabaseAnon)
			return request, nil
		},
		RefreshErrorHandler: func(err error) {
			h.jwksInitError = err
		},
	})
	if err != nil {
		h.jwksInitError = err
		return nil, err
	}
	h.jwks = jwks
	h.jwksURL = jwksURL
	h.jwksInitError = nil
	_ = ctx
	return h.jwks, nil
}

func (h *eventsStreamHandler) lookupOrgIDByUserID(_ context.Context, userID string) (string, error) {
	type orgScopeRow struct {
		OrgID string `json:"org_id"`
	}
	var rows []orgScopeRow
	_, err := h.clients.AdminPostgrest().
		From("users").
		Select("org_id", "", false).
		Eq("id", userID).
		Limit(1, "").
		ExecuteTo(&rows)
	if err != nil {
		return "", fmt.Errorf("failed to resolve organization scope")
	}
	if len(rows) != 1 {
		return "", fmt.Errorf("organization scope is missing")
	}
	orgID := strings.TrimSpace(rows[0].OrgID)
	if orgID == "" || len(orgID) > maxSSEOrgIDLength {
		return "", fmt.Errorf("organization scope is invalid")
	}
	if _, err := uuid.Parse(orgID); err != nil {
		return "", fmt.Errorf("organization scope is invalid")
	}
	return orgID, nil
}

func (h *eventsStreamHandler) pollRecentUpdates(_ context.Context, orgID string, since time.Time) ([]executionStreamRow, []reviewQueueStreamRow, []attestationStreamRow, error) {
	cleanOrgID := strings.TrimSpace(orgID)
	if _, err := uuid.Parse(cleanOrgID); err != nil {
		return nil, nil, nil, fmt.Errorf("organization scope is invalid")
	}
	sinceRFC := since.UTC().Format(time.RFC3339Nano)

	var startedExecutions []executionStreamRow
	_, err := h.clients.AdminPostgrest().
		From("executions").
		Select("id,org_id,bot_id,repo_id,status,requires_review,started_at,completed_at", "", false).
		Eq("org_id", cleanOrgID).
		Gte("started_at", sinceRFC).
		Order("started_at", &postgrest.OrderOpts{Ascending: false}).
		Limit(100, "").
		ExecuteTo(&startedExecutions)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query recent execution starts: %w", err)
	}

	var completedExecutions []executionStreamRow
	_, err = h.clients.AdminPostgrest().
		From("executions").
		Select("id,org_id,bot_id,repo_id,status,requires_review,started_at,completed_at", "", false).
		Eq("org_id", cleanOrgID).
		Gte("completed_at", sinceRFC).
		Order("completed_at", &postgrest.OrderOpts{Ascending: false}).
		Limit(100, "").
		ExecuteTo(&completedExecutions)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query recent execution completions: %w", err)
	}
	executions := mergeExecutionRows(startedExecutions, completedExecutions)

	var queueRows []reviewQueueStreamRow
	_, err = h.clients.AdminPostgrest().
		From("review_queue").
		Select("id,org_id,execution_id,status,reviewed_at,created_at,diff_content,claude_summary", "", false).
		Eq("org_id", cleanOrgID).
		Eq("status", "pending").
		Gte("created_at", sinceRFC).
		Order("created_at", &postgrest.OrderOpts{Ascending: false}).
		Limit(100, "").
		ExecuteTo(&queueRows)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query recent review queue items: %w", err)
	}

	var attestations []attestationStreamRow
	_, err = h.clients.AdminPostgrest().
		From("attestations").
		Select("id,org_id,execution_id,artifact_hash,rekor_log_index,rekor_uuid,signed_at", "", false).
		Eq("org_id", cleanOrgID).
		Gte("signed_at", sinceRFC).
		Order("signed_at", &postgrest.OrderOpts{Ascending: false}).
		Limit(100, "").
		ExecuteTo(&attestations)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query recent attestations: %w", err)
	}

	return executions, queueRows, attestations, nil
}

func mergeExecutionRows(first []executionStreamRow, second []executionStreamRow) []executionStreamRow {
	if len(first) == 0 && len(second) == 0 {
		return []executionStreamRow{}
	}
	merged := make(map[string]executionStreamRow, len(first)+len(second))
	for _, row := range first {
		merged[strings.TrimSpace(row.ID)] = row
	}
	for _, row := range second {
		merged[strings.TrimSpace(row.ID)] = row
	}
	result := make([]executionStreamRow, 0, len(merged))
	for _, row := range merged {
		result = append(result, row)
	}
	return result
}

func writeHeartbeat(writer http.ResponseWriter, flusher http.Flusher) error {
	if _, err := fmt.Fprint(writer, ": heartbeat\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeSSEEvent(writer http.ResponseWriter, flusher http.Flusher, event string, payload any) error {
	if strings.TrimSpace(event) == "" {
		return fmt.Errorf("event type is required")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
