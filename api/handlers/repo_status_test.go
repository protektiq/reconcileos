package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRepoStatus_ServiceNotConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("org_id", "e9f7c00b-d6f7-4cc7-8ddf-07f8d9834460")
		c.Next()
	})
	router.GET("/api/v1/repos/:repo_full_name/status", RepoStatus(nil))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/repos/acme-service/status", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected %d, got %d", http.StatusInternalServerError, resp.Code)
	}
}

func TestRepoStatus_InvalidRepoFullName(t *testing.T) {
	if err := validateRepoFullName("invalid"); err == nil {
		t.Fatalf("expected validation error")
	}
	if err := validateRepoFullName("acme/service"); err != nil {
		t.Fatalf("expected valid repo name, got %v", err)
	}
}

func TestTriggerExecution_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("org_id", "e9f7c00b-d6f7-4cc7-8ddf-07f8d9834460")
		c.Next()
	})
	router.POST("/api/v1/executions/trigger", TriggerExecution(nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/executions/trigger", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected %d, got %d", http.StatusInternalServerError, resp.Code)
	}
}

func TestExecutionStatus_InvalidExecutionID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("org_id", "e9f7c00b-d6f7-4cc7-8ddf-07f8d9834460")
		c.Next()
	})
	router.GET("/api/v1/executions/:id/status", ExecutionStatus(nil))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/executions/not-a-uuid/status", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected %d, got %d", http.StatusInternalServerError, resp.Code)
	}
}
