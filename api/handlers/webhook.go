package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"reconcileos.dev/api/db"
	"reconcileos.dev/api/services"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const (
	maxGitHubSignatureHeaderLength = 256
	maxGitHubEventHeaderLength     = 64
	maxGitHubDeliveryHeaderLength  = 128
	maxWebhookBodyBytes            = 2 * 1024 * 1024
)

type githubWebhookEnvelope struct {
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

type githubWebhookErrorResponse struct {
	Error string `json:"error"`
}

func GitHubWebhook(
	clients *db.SupabaseClients,
	githubService *services.GitHubService,
	webhookSecret string,
) gin.HandlerFunc {
	cleanSecret := strings.TrimSpace(webhookSecret)
	return func(c *gin.Context) {
		if clients == nil || githubService == nil || cleanSecret == "" {
			log.Error().
				Str("request_id", c.GetHeader("X-Request-ID")).
				Msg("github_webhook_handler_not_configured")
			c.AbortWithStatusJSON(http.StatusInternalServerError, githubWebhookErrorResponse{Error: "webhook not configured"})
			return
		}

		requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		signatureHeader := strings.TrimSpace(c.GetHeader("X-Hub-Signature-256"))
		if signatureHeader == "" || len(signatureHeader) > maxGitHubSignatureHeaderLength {
			log.Warn().
				Str("request_id", requestID).
				Str("reason", "missing_or_invalid_signature_header").
				Msg("github_webhook_signature_rejected")
			c.AbortWithStatusJSON(http.StatusUnauthorized, githubWebhookErrorResponse{Error: "invalid webhook signature"})
			return
		}

		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodyBytes))
		if err != nil {
			log.Warn().
				Str("request_id", requestID).
				Err(err).
				Str("reason", "read_body_failed").
				Msg("github_webhook_signature_rejected")
			c.AbortWithStatusJSON(http.StatusUnauthorized, githubWebhookErrorResponse{Error: "invalid webhook signature"})
			return
		}
		if len(body) == maxWebhookBodyBytes {
			log.Warn().
				Str("request_id", requestID).
				Str("reason", "body_too_large").
				Msg("github_webhook_signature_rejected")
			c.AbortWithStatusJSON(http.StatusUnauthorized, githubWebhookErrorResponse{Error: "invalid webhook signature"})
			return
		}

		if err := validateGitHubWebhookSignature(body, signatureHeader, cleanSecret); err != nil {
			log.Warn().
				Str("request_id", requestID).
				Str("reason", err.Error()).
				Msg("github_webhook_signature_rejected")
			c.AbortWithStatusJSON(http.StatusUnauthorized, githubWebhookErrorResponse{Error: "invalid webhook signature"})
			return
		}

		eventType := strings.TrimSpace(c.GetHeader("X-GitHub-Event"))
		if eventType == "" || len(eventType) > maxGitHubEventHeaderLength {
			eventType = "unknown"
		}
		deliveryID := strings.TrimSpace(c.GetHeader("X-GitHub-Delivery"))
		if len(deliveryID) > maxGitHubDeliveryHeaderLength {
			deliveryID = ""
		}

		c.Status(http.StatusOK)

		go processGitHubWebhookEvent(clients, githubService, eventType, deliveryID, body, requestID)
	}
}

func validateGitHubWebhookSignature(body []byte, signatureHeader, webhookSecret string) error {
	if len(body) == 0 {
		return errors.New("empty_payload")
	}
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return errors.New("missing_sha256_prefix")
	}

	encodedSignature := strings.TrimSpace(strings.TrimPrefix(signatureHeader, "sha256="))
	if encodedSignature == "" {
		return errors.New("empty_signature")
	}

	providedSignature, err := hex.DecodeString(encodedSignature)
	if err != nil {
		return errors.New("signature_not_hex")
	}

	mac := hmac.New(sha256.New, []byte(webhookSecret))
	_, _ = mac.Write(body)
	expectedSignature := mac.Sum(nil)

	if !hmac.Equal(expectedSignature, providedSignature) {
		return errors.New("signature_mismatch")
	}

	return nil
}

func processGitHubWebhookEvent(
	clients *db.SupabaseClients,
	githubService *services.GitHubService,
	eventType string,
	deliveryID string,
	body []byte,
	requestID string,
) {
	if eventType != "push" && eventType != "installation" && eventType != "installation_repositories" {
		log.Info().
			Str("event_type", eventType).
			Str("delivery_id", deliveryID).
			Str("request_id", requestID).
			Msg("github_webhook_event_ignored")
		return
	}

	installationID, err := extractInstallationID(body)
	if err != nil {
		log.Warn().
			Str("event_type", eventType).
			Str("delivery_id", deliveryID).
			Str("request_id", requestID).
			Err(err).
			Msg("github_webhook_missing_installation")
		return
	}

	orgID, err := resolveOrgIDForInstallation(context.Background(), clients, githubService, installationID)
	if err != nil {
		log.Warn().
			Str("event_type", eventType).
			Str("delivery_id", deliveryID).
			Str("request_id", requestID).
			Int64("installation_id", installationID).
			Err(err).
			Msg("github_webhook_org_resolution_failed")
		return
	}

	if err := insertGitHubEvent(context.Background(), clients, orgID, eventType, body); err != nil {
		log.Error().
			Str("event_type", eventType).
			Str("delivery_id", deliveryID).
			Str("request_id", requestID).
			Int64("installation_id", installationID).
			Err(err).
			Msg("github_webhook_event_insert_failed")
		return
	}

	log.Info().
		Str("event_type", eventType).
		Str("delivery_id", deliveryID).
		Str("request_id", requestID).
		Str("org_id", orgID).
		Int64("installation_id", installationID).
		Msg("github_webhook_event_recorded")
}

func extractInstallationID(body []byte) (int64, error) {
	var envelope githubWebhookEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0, fmt.Errorf("decode webhook payload: %w", err)
	}
	if envelope.Installation.ID <= 0 {
		return 0, errors.New("installation_id_missing")
	}

	return envelope.Installation.ID, nil
}

func resolveOrgIDForInstallation(
	_ context.Context,
	clients *db.SupabaseClients,
	githubService *services.GitHubService,
	installationID int64,
) (string, error) {
	type installRow struct {
		OrgID string `json:"org_id"`
	}

	var mapping installRow
	_, err := clients.AdminPostgrest().
		From("github_installations").
		Select("org_id", "", false).
		Eq("installation_id", fmt.Sprintf("%d", installationID)).
		Limit(1, "").
		Single().
		ExecuteTo(&mapping)
	if err == nil {
		orgID := strings.TrimSpace(mapping.OrgID)
		if orgID != "" {
			return orgID, nil
		}
	}

	orgSlug, lookupErr := githubService.GetInstallationOrgSlug(installationID)
	if lookupErr != nil {
		return "", lookupErr
	}

	type orgRow struct {
		ID string `json:"id"`
	}
	var org orgRow
	_, orgErr := clients.AdminPostgrest().
		From("orgs").
		Select("id", "", false).
		Eq("github_org_slug", orgSlug).
		Limit(1, "").
		Single().
		ExecuteTo(&org)
	if orgErr != nil {
		return "", orgErr
	}
	orgID := strings.TrimSpace(org.ID)
	if orgID == "" {
		return "", errors.New("resolved org ID is empty")
	}

	record := map[string]any{
		"org_id":          orgID,
		"installation_id": installationID,
		"account_login":   orgSlug,
		"updated_at":      time.Now().UTC().Format(time.RFC3339Nano),
	}

	_, _, upsertErr := clients.AdminPostgrest().
		From("github_installations").
		Insert(record, true, "installation_id", "", "").
		Execute()
	if upsertErr != nil {
		return "", upsertErr
	}

	return orgID, nil
}

func insertGitHubEvent(_ context.Context, clients *db.SupabaseClients, orgID, eventType string, body []byte) error {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode payload for storage: %w", err)
	}

	eventRecord := map[string]any{
		"org_id":     orgID,
		"event_type": eventType,
		"payload":    payload,
		"processed":  false,
	}

	_, _, err := clients.AdminPostgrest().
		From("events").
		Insert(eventRecord, false, "", "", "").
		Execute()
	if err != nil {
		return fmt.Errorf("insert event record: %w", err)
	}

	return nil
}
