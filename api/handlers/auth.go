package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"reconcileos.dev/api/db"
	"reconcileos.dev/api/services"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"github.com/supabase-community/gotrue-go"
	"github.com/supabase-community/gotrue-go/types"
)

const (
	maxOAuthCodeLength  = 1024
	maxOAuthStateLength = 1024
	maxGitHubLoginLen   = 255
	maxGitHubEmailLen   = 320
)

type githubOAuthCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

type githubOAuthCallbackResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type githubOAuthCallbackErrorResponse struct {
	Error string `json:"error"`
}

func GitHubOAuthCallback(
	clients *db.SupabaseClients,
	githubService *services.GitHubService,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if clients == nil || githubService == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, githubOAuthCallbackErrorResponse{Error: "github auth not configured"})
			return
		}

		var request githubOAuthCallbackRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, githubOAuthCallbackErrorResponse{Error: "invalid request body"})
			return
		}

		cleanCode := strings.TrimSpace(request.Code)
		cleanState := strings.TrimSpace(request.State)
		if cleanCode == "" || len(cleanCode) > maxOAuthCodeLength {
			c.AbortWithStatusJSON(http.StatusBadRequest, githubOAuthCallbackErrorResponse{Error: "invalid oauth code"})
			return
		}
		if len(cleanState) > maxOAuthStateLength {
			c.AbortWithStatusJSON(http.StatusBadRequest, githubOAuthCallbackErrorResponse{Error: "invalid oauth state"})
			return
		}

		githubAccessToken, err := githubService.ExchangeOAuthCode(cleanCode, cleanState)
		if err != nil {
			log.Warn().Err(err).Str("request_id", c.GetHeader("X-Request-ID")).Msg("github_oauth_code_exchange_failed")
			c.AbortWithStatusJSON(http.StatusBadGateway, githubOAuthCallbackErrorResponse{Error: "github oauth exchange failed"})
			return
		}

		githubUser, err := githubService.GetAuthenticatedUser(githubAccessToken)
		if err != nil {
			log.Warn().Err(err).Str("request_id", c.GetHeader("X-Request-ID")).Msg("github_oauth_user_fetch_failed")
			c.AbortWithStatusJSON(http.StatusBadGateway, githubOAuthCallbackErrorResponse{Error: "github user lookup failed"})
			return
		}

		login := strings.TrimSpace(githubUser.Login)
		if login == "" || len(login) > maxGitHubLoginLen {
			c.AbortWithStatusJSON(http.StatusBadRequest, githubOAuthCallbackErrorResponse{Error: "github login is invalid"})
			return
		}

		email := strings.TrimSpace(githubUser.Email)
		if email == "" {
			email = fmt.Sprintf("%s@users.noreply.github.com", login)
		}
		if len(email) > maxGitHubEmailLen || !strings.Contains(email, "@") {
			c.AbortWithStatusJSON(http.StatusBadRequest, githubOAuthCallbackErrorResponse{Error: "github email is invalid"})
			return
		}

		supabaseSession, err := issueSupabaseSessionForEmail(clients, email, login, githubUser.ID)
		if err != nil {
			log.Error().Err(err).Str("request_id", c.GetHeader("X-Request-ID")).Msg("supabase_session_issue_failed")
			c.AbortWithStatusJSON(http.StatusBadGateway, githubOAuthCallbackErrorResponse{Error: "supabase session issue failed"})
			return
		}

		orgID, err := ensureOrgForGitHubLogin(clients, login)
		if err != nil {
			log.Error().Err(err).Str("request_id", c.GetHeader("X-Request-ID")).Msg("supabase_org_upsert_failed")
			c.AbortWithStatusJSON(http.StatusBadGateway, githubOAuthCallbackErrorResponse{Error: "organization setup failed"})
			return
		}

		if err := upsertUserRow(clients, supabaseSession.User.ID.String(), orgID); err != nil {
			log.Error().Err(err).Str("request_id", c.GetHeader("X-Request-ID")).Msg("supabase_user_upsert_failed")
			c.AbortWithStatusJSON(http.StatusBadGateway, githubOAuthCallbackErrorResponse{Error: "user setup failed"})
			return
		}

		c.JSON(http.StatusOK, githubOAuthCallbackResponse{
			AccessToken:  supabaseSession.AccessToken,
			RefreshToken: supabaseSession.RefreshToken,
			TokenType:    supabaseSession.TokenType,
			ExpiresIn:    supabaseSession.ExpiresIn,
		})
	}
}

func issueSupabaseSessionForEmail(clients *db.SupabaseClients, email, login string, githubUserID int64) (*types.Session, error) {
	projectReference, err := extractSupabaseProjectReference(clients.URL)
	if err != nil {
		return nil, err
	}

	authClient := gotrue.New(projectReference, clients.AnonKey).WithToken(clients.ServiceRoleKey)
	_, err = authClient.AdminGenerateLink(types.AdminGenerateLinkRequest{
		Type:  types.LinkTypeSignup,
		Email: email,
		Data: map[string]any{
			"github_login":   login,
			"github_user_id": githubUserID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("admin signup link generation failed: %w", err)
	}

	loginLink, err := authClient.AdminGenerateLink(types.AdminGenerateLinkRequest{
		Type:  types.LinkTypeMagicLink,
		Email: email,
		Data: map[string]any{
			"github_login":   login,
			"github_user_id": githubUserID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("admin magic link generation failed: %w", err)
	}

	if strings.TrimSpace(loginLink.EmailOTP) == "" {
		return nil, fmt.Errorf("magic link generation did not return email otp")
	}

	verifyResponse, err := authClient.VerifyForUser(types.VerifyForUserRequest{
		Type:  types.VerificationTypeMagiclink,
		Token: strings.TrimSpace(loginLink.EmailOTP),
		Email: email,
	})
	if err != nil {
		return nil, fmt.Errorf("verify magic link failed: %w", err)
	}
	if strings.TrimSpace(verifyResponse.AccessToken) == "" || strings.TrimSpace(verifyResponse.RefreshToken) == "" {
		return nil, fmt.Errorf("supabase verify response missing session tokens")
	}

	return &verifyResponse.Session, nil
}

func extractSupabaseProjectReference(supabaseURL string) (string, error) {
	parsedURL, err := url.Parse(strings.TrimSpace(supabaseURL))
	if err != nil {
		return "", fmt.Errorf("parse supabase url: %w", err)
	}
	if parsedURL.Host == "" {
		return "", fmt.Errorf("supabase URL host is empty")
	}

	hostParts := strings.Split(parsedURL.Host, ".")
	if len(hostParts) == 0 || strings.TrimSpace(hostParts[0]) == "" {
		return "", fmt.Errorf("supabase project reference missing from host")
	}

	return strings.TrimSpace(hostParts[0]), nil
}

func ensureOrgForGitHubLogin(clients *db.SupabaseClients, login string) (string, error) {
	type orgRow struct {
		ID string `json:"id"`
	}

	var existing orgRow
	_, err := clients.AdminPostgrest().
		From("orgs").
		Select("id", "", false).
		Eq("github_org_slug", login).
		Limit(1, "").
		Single().
		ExecuteTo(&existing)
	if err == nil && strings.TrimSpace(existing.ID) != "" {
		return strings.TrimSpace(existing.ID), nil
	}

	record := map[string]any{
		"name":            login,
		"github_org_slug": login,
	}
	_, _, insertErr := clients.AdminPostgrest().
		From("orgs").
		Insert(record, true, "github_org_slug", "representation", "").
		Execute()
	if insertErr != nil {
		return "", insertErr
	}

	var inserted orgRow
	_, selectErr := clients.AdminPostgrest().
		From("orgs").
		Select("id", "", false).
		Eq("github_org_slug", login).
		Limit(1, "").
		Single().
		ExecuteTo(&inserted)
	if selectErr != nil {
		return "", selectErr
	}

	orgID := strings.TrimSpace(inserted.ID)
	if orgID == "" {
		return "", fmt.Errorf("org lookup returned empty id")
	}

	return orgID, nil
}

func upsertUserRow(clients *db.SupabaseClients, userID, orgID string) error {
	cleanUserID := strings.TrimSpace(userID)
	cleanOrgID := strings.TrimSpace(orgID)
	if cleanUserID == "" || cleanOrgID == "" {
		return fmt.Errorf("user ID and org ID are required")
	}

	record := map[string]any{
		"id":         cleanUserID,
		"org_id":     cleanOrgID,
		"role":       "member",
		"created_at": time.Now().UTC().Format(time.RFC3339Nano),
	}

	_, _, err := clients.AdminPostgrest().
		From("users").
		Insert(record, true, "id", "", "").
		Execute()
	if err != nil {
		return fmt.Errorf("upsert users row: %w", err)
	}

	return nil
}
