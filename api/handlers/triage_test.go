package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reconcileos.dev/api/services"

	"github.com/gin-gonic/gin"
)

func TestTriageScore_ServiceNotConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/triage/score", TriageScore(nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/triage/score", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected %d, got %d", http.StatusInternalServerError, resp.Code)
	}
}

func TestTriageScore_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("org_id", "e9f7c00b-d6f7-4cc7-8ddf-07f8d9834460")
		c.Next()
	})

	anthropicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"contextual_exploitability\":5.2,\"blast_radius\":\"MEDIUM\",\"recommended_action\":\"auto-patch\",\"reasoning\":\"The vulnerable package is reachable through a transitive path but mitigation is straightforward. Patch and validate core integration tests.\",\"priority_rank\":\"P2\"}"}]}`))
	}))
	defer anthropicServer.Close()

	triageService, err := services.NewTriageService("test-key", anthropicServer.URL, "claude-sonnet-4-20250514")
	if err != nil {
		t.Fatalf("failed to create triage service: %v", err)
	}

	router.POST("/api/v1/triage/score", TriageScore(triageService))

	body := `{
		"cve_id":"CVE-2026-1234",
		"cvss_score":8.1,
		"cvss_vector":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
		"affected_package":"github.com/example/pkg",
		"affected_version":"1.0.0",
		"repo_full_name":"acme/service",
		"dependency_tree":["acme/api","acme/web"],
		"tech_stack_context":"Go, Gin, Supabase"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/triage/score", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d body=%s", http.StatusOK, resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"priority_rank":"P2"`) {
		t.Fatalf("expected triage response body, got %s", resp.Body.String())
	}
}
