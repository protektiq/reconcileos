package handlers

import (
	"net/http"
	"strings"
	"time"

	"reconcileos.dev/api/middleware"
	"reconcileos.dev/api/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	maxAttestationIDLength = 64
	maxDateParamLength     = 64
)

type attestationsErrorResponse struct {
	Error string `json:"error"`
}

type verifyAttestationRequest struct {
	ArtifactHash string `json:"artifact_hash"`
}

func ListAttestations(attestationService *services.AttestationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if attestationService == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, attestationsErrorResponse{Error: "attestation service not configured"})
			return
		}
		orgID := strings.TrimSpace(middleware.MustOrgID(c))

		page, err := services.ParseAttestationPage(c.Query("page"))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, attestationsErrorResponse{Error: err.Error()})
			return
		}

		repoID := strings.TrimSpace(c.Query("repo_id"))
		executionID := strings.TrimSpace(c.Query("execution_id"))
		startDateRaw := strings.TrimSpace(c.Query("start_date"))
		endDateRaw := strings.TrimSpace(c.Query("end_date"))

		if len(startDateRaw) > maxDateParamLength || len(endDateRaw) > maxDateParamLength {
			c.AbortWithStatusJSON(http.StatusBadRequest, attestationsErrorResponse{Error: "date filters are too long"})
			return
		}

		var startDate *time.Time
		if startDateRaw != "" {
			parsed, parseErr := time.Parse(time.RFC3339, startDateRaw)
			if parseErr != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, attestationsErrorResponse{Error: "start_date must be RFC3339"})
				return
			}
			startDate = &parsed
		}

		var endDate *time.Time
		if endDateRaw != "" {
			parsed, parseErr := time.Parse(time.RFC3339, endDateRaw)
			if parseErr != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, attestationsErrorResponse{Error: "end_date must be RFC3339"})
				return
			}
			endDate = &parsed
		}

		records, listErr := attestationService.ListAttestationsForOrg(c.Request.Context(), orgID, services.AttestationFilters{
			RepoID:      repoID,
			StartDate:   startDate,
			EndDate:     endDate,
			ExecutionID: executionID,
			Page:        page,
			PageSize:    50,
		})
		if listErr != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, attestationsErrorResponse{Error: listErr.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"page":         page,
			"page_size":    50,
			"attestations": records,
		})
	}
}

func GetAttestation(attestationService *services.AttestationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if attestationService == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, attestationsErrorResponse{Error: "attestation service not configured"})
			return
		}
		orgID := strings.TrimSpace(middleware.MustOrgID(c))
		attestationID := strings.TrimSpace(c.Param("id"))
		if attestationID == "" || len(attestationID) > maxAttestationIDLength {
			c.AbortWithStatusJSON(http.StatusBadRequest, attestationsErrorResponse{Error: "attestation id is invalid"})
			return
		}
		if _, err := uuid.Parse(attestationID); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, attestationsErrorResponse{Error: "attestation id is invalid"})
			return
		}

		record, err := attestationService.GetAttestationForOrg(c.Request.Context(), orgID, attestationID)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				c.AbortWithStatusJSON(http.StatusNotFound, attestationsErrorResponse{Error: "attestation not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusBadRequest, attestationsErrorResponse{Error: err.Error()})
			return
		}

		c.JSON(http.StatusOK, record)
	}
}

func VerifyAttestation(attestationService *services.AttestationService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if attestationService == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, attestationsErrorResponse{Error: "attestation service not configured"})
			return
		}
		var request verifyAttestationRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, attestationsErrorResponse{Error: "invalid request body"})
			return
		}

		records, err := attestationService.VerifyAttestation(c.Request.Context(), request.ArtifactHash)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, attestationsErrorResponse{Error: err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"records": records,
		})
	}
}
