package handlers

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"reconcileos.dev/api/db"
	"reconcileos.dev/api/services"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const (
	maxOrgIDLength = 64
)

var orgIDPattern = regexp.MustCompile(`^[a-fA-F0-9-]+$`)

type githubInstallationTokenResponse struct {
	Token string `json:"token"`
}

type githubInstallationTokenErrorResponse struct {
	Error string `json:"error"`
}

type githubInstallationRecord struct {
	InstallationID int64 `json:"installation_id"`
}

func GitHubInstallationToken(clients *db.SupabaseClients, githubService *services.GitHubService) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if clients == nil || githubService == nil {
			log.Error().
				Str("request_id", requestID).
				Msg("github_installation_token_handler_not_configured")
			c.AbortWithStatusJSON(http.StatusInternalServerError, githubInstallationTokenErrorResponse{Error: "github token endpoint not configured"})
			return
		}

		rawOrgID, ok := c.Get("org_id")
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, githubInstallationTokenErrorResponse{Error: "org scope is missing"})
			return
		}

		orgID, ok := rawOrgID.(string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, githubInstallationTokenErrorResponse{Error: "org scope is invalid"})
			return
		}
		orgID = strings.TrimSpace(orgID)
		if orgID == "" || len(orgID) > maxOrgIDLength || !orgIDPattern.MatchString(orgID) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, githubInstallationTokenErrorResponse{Error: "org scope is invalid"})
			return
		}

		installationID, lookupErr := lookupInstallationIDByOrg(clients, orgID)
		if lookupErr != nil {
			log.Warn().
				Err(lookupErr).
				Str("request_id", requestID).
				Str("org_id", orgID).
				Msg("github_installation_lookup_failed")
			switch {
			case strings.Contains(lookupErr.Error(), "multiple installations"):
				c.AbortWithStatusJSON(http.StatusConflict, githubInstallationTokenErrorResponse{Error: "multiple github installations found for org"})
			case strings.Contains(lookupErr.Error(), "not found"):
				c.AbortWithStatusJSON(http.StatusNotFound, githubInstallationTokenErrorResponse{Error: "github installation not found for org"})
			default:
				c.AbortWithStatusJSON(http.StatusBadGateway, githubInstallationTokenErrorResponse{Error: "github installation lookup failed"})
			}
			return
		}

		token, err := githubService.GenerateInstallationToken(installationID)
		if err != nil {
			log.Warn().
				Err(err).
				Str("request_id", requestID).
				Str("org_id", orgID).
				Int64("installation_id", installationID).
				Msg("github_installation_token_generation_failed")
			c.AbortWithStatusJSON(http.StatusBadGateway, githubInstallationTokenErrorResponse{Error: "github installation token generation failed"})
			return
		}

		c.JSON(http.StatusOK, githubInstallationTokenResponse{Token: token})
	}
}

func lookupInstallationIDByOrg(clients *db.SupabaseClients, orgID string) (int64, error) {
	var installations []githubInstallationRecord
	_, err := clients.AdminPostgrest().
		From("github_installations").
		Select("installation_id", "", false).
		Eq("org_id", orgID).
		Limit(2, "").
		ExecuteTo(&installations)
	if err != nil {
		return 0, fmt.Errorf("lookup github installation for org: %w", err)
	}
	if len(installations) == 0 {
		return 0, fmt.Errorf("installation not found")
	}
	if len(installations) > 1 {
		return 0, fmt.Errorf("multiple installations found for org")
	}

	installationID := installations[0].InstallationID
	if installationID <= 0 {
		return 0, fmt.Errorf("installation_id is invalid")
	}

	return installationID, nil
}
