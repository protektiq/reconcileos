package handlers

import (
	"net/http"
	"strings"

	"reconcileos.dev/api/middleware"
	"reconcileos.dev/api/services"

	"github.com/gin-gonic/gin"
)

const maxTriageRequestBytes = 32768

type triageErrorResponse struct {
	Error string `json:"error"`
}

type triageScoreRequest struct {
	CVEID            string   `json:"cve_id"`
	CVSSScore        float64  `json:"cvss_score"`
	CVSSVector       string   `json:"cvss_vector"`
	AffectedPackage  string   `json:"affected_package"`
	AffectedVersion  string   `json:"affected_version"`
	RepoFullName     string   `json:"repo_full_name"`
	DependencyTree   []string `json:"dependency_tree"`
	TechStackContext string   `json:"tech_stack_context"`
}

func TriageScore(triageService *services.TriageService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if triageService == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, triageErrorResponse{Error: "triage service not configured"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxTriageRequestBytes)

		var request triageScoreRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, triageErrorResponse{Error: "invalid request body"})
			return
		}

		orgID := strings.TrimSpace(middleware.MustOrgID(c))
		input := services.TriageInput{
			CVEID:            request.CVEID,
			CVSSScore:        request.CVSSScore,
			CVSSVector:       request.CVSSVector,
			AffectedPackage:  request.AffectedPackage,
			AffectedVersion:  request.AffectedVersion,
			OrgID:            orgID,
			RepoFullName:     request.RepoFullName,
			DependencyTree:   request.DependencyTree,
			TechStackContext: request.TechStackContext,
		}

		result, err := triageService.ScoreCVE(c.Request.Context(), input)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, triageErrorResponse{Error: err.Error()})
			return
		}

		c.JSON(http.StatusOK, result)
	}
}
