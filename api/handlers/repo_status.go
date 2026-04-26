package handlers

import (
	"net/http"
	"strings"

	"reconcileos.dev/api/middleware"
	"reconcileos.dev/api/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	maxRepoNameLength      = 255
	maxExecutionIDParamLen = 64
)

type repoStatusErrorResponse struct {
	Error string `json:"error"`
}

type triggerExecutionRequest struct {
	BotID        string `json:"bot_id"`
	RepoFullName string `json:"repo_full_name"`
	DryRun       bool   `json:"dry_run"`
}

type triggerExecutionResponse struct {
	ExecutionID string `json:"execution_id"`
}

func RepoStatus(service *services.ExecutionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, repoStatusErrorResponse{Error: "execution service not configured"})
			return
		}
		orgID := strings.TrimSpace(middleware.MustOrgID(c))
		repoFullName := strings.TrimSpace(c.Param("repo_full_name"))
		if err := validateRepoFullName(repoFullName); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: err.Error()})
			return
		}

		record, err := service.GetRepoStatus(c.Request.Context(), orgID, repoFullName)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				c.AbortWithStatusJSON(http.StatusNotFound, repoStatusErrorResponse{Error: "repository not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, record)
	}
}

func TriggerExecution(service *services.ExecutionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, repoStatusErrorResponse{Error: "execution service not configured"})
			return
		}
		orgID := strings.TrimSpace(middleware.MustOrgID(c))
		if _, err := uuid.Parse(orgID); err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, repoStatusErrorResponse{Error: "invalid organization scope"})
			return
		}

		var request triggerExecutionRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: "invalid request body"})
			return
		}
		request.BotID = strings.TrimSpace(request.BotID)
		request.RepoFullName = strings.TrimSpace(request.RepoFullName)
		if request.BotID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: "bot id is required"})
			return
		}
		if len(request.BotID) > 64 {
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: "bot id is invalid"})
			return
		}
		if _, err := uuid.Parse(request.BotID); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: "bot id is invalid"})
			return
		}
		if err := validateRepoFullName(request.RepoFullName); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: err.Error()})
			return
		}

		executionID, err := service.TriggerExecution(c.Request.Context(), services.TriggerExecutionInput{
			OrgID:        orgID,
			BotID:        request.BotID,
			RepoFullName: request.RepoFullName,
			DryRun:       request.DryRun,
		})
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				c.AbortWithStatusJSON(http.StatusNotFound, repoStatusErrorResponse{Error: "repository not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, triggerExecutionResponse{ExecutionID: executionID})
	}
}

func ExecutionStatus(service *services.ExecutionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if service == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, repoStatusErrorResponse{Error: "execution service not configured"})
			return
		}
		orgID := strings.TrimSpace(middleware.MustOrgID(c))
		executionID := strings.TrimSpace(c.Param("id"))
		if executionID == "" || len(executionID) > maxExecutionIDParamLen {
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: "execution id is invalid"})
			return
		}
		if _, err := uuid.Parse(executionID); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: "execution id is invalid"})
			return
		}

		record, err := service.GetExecutionStatus(c.Request.Context(), orgID, executionID)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				c.AbortWithStatusJSON(http.StatusNotFound, repoStatusErrorResponse{Error: "execution not found"})
				return
			}
			c.AbortWithStatusJSON(http.StatusBadRequest, repoStatusErrorResponse{Error: err.Error()})
			return
		}
		c.JSON(http.StatusOK, record)
	}
}

func validateRepoFullName(repoFullName string) error {
	if repoFullName == "" || len(repoFullName) > maxRepoNameLength {
		return &validationError{message: "repository full name is invalid"}
	}
	parts := strings.Split(repoFullName, "/")
	if len(parts) != 2 {
		return &validationError{message: "repository full name is invalid"}
	}
	owner := strings.TrimSpace(parts[0])
	name := strings.TrimSpace(parts[1])
	if owner == "" || name == "" {
		return &validationError{message: "repository full name is invalid"}
	}
	return nil
}

type validationError struct {
	message string
}

func (e *validationError) Error() string {
	return e.message
}
