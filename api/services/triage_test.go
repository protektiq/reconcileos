package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScoreCVE_SendsMetadataAndParsesResponse(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if got := strings.TrimSpace(r.Header.Get("x-api-key")); got != "test-key" {
			t.Fatalf("expected api key header, got %q", got)
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}

		if payload["max_tokens"] != float64(anthropicMaxTokens) {
			t.Fatalf("expected max_tokens %d, got %#v", anthropicMaxTokens, payload["max_tokens"])
		}
		metadata, ok := payload["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("metadata was not an object")
		}
		if strings.TrimSpace(metadata["org_id"].(string)) != "e9f7c00b-d6f7-4cc7-8ddf-07f8d9834460" {
			t.Fatalf("org_id metadata missing")
		}

		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"contextual_exploitability\":7.5,\"blast_radius\":\"HIGH\",\"recommended_action\":\"human-review\",\"reasoning\":\"This package is transitively reachable in production workloads and public exploit guidance exists. Patch quickly and validate runtime behavior in integration tests.\",\"priority_rank\":\"P1\"}"}]}`))
	}))
	defer server.Close()

	service, err := NewTriageService("test-key", server.URL, defaultAnthropicModel)
	if err != nil {
		t.Fatalf("NewTriageService returned error: %v", err)
	}

	result, err := service.ScoreCVE(context.Background(), TriageInput{
		CVEID:            "CVE-2026-12345",
		CVSSScore:        9.8,
		CVSSVector:       "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
		AffectedPackage:  "example/pkg",
		AffectedVersion:  "1.2.3",
		OrgID:            "e9f7c00b-d6f7-4cc7-8ddf-07f8d9834460",
		RepoFullName:     "acme/service",
		DependencyTree:   []string{"acme/api", "acme/web"},
		TechStackContext: "Go services with Gin and Supabase",
	})
	if err != nil {
		t.Fatalf("ScoreCVE returned error: %v", err)
	}

	if result.PriorityRank != "P1" {
		t.Fatalf("expected P1 priority, got %s", result.PriorityRank)
	}
	if result.RecommendedAction != "human-review" {
		t.Fatalf("expected human-review action, got %s", result.RecommendedAction)
	}
}

func TestGeneratePRSummary_UsesContextOrgID(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		metadata := payload["metadata"].(map[string]any)
		if strings.TrimSpace(metadata["org_id"].(string)) == "" {
			t.Fatalf("expected org_id in metadata")
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"Updated vulnerable dependencies and adjusted versions. This mitigates the exploitable path for the CVE. Test dependency resolution and full CI before merge. Breaking risk is low but verify integration behavior."}]}`))
	}))
	defer server.Close()

	service, err := NewTriageService("test-key", server.URL, defaultAnthropicModel)
	if err != nil {
		t.Fatalf("NewTriageService returned error: %v", err)
	}

	ctx := context.WithValue(context.Background(), "org_id", "e9f7c00b-d6f7-4cc7-8ddf-07f8d9834460")
	summary, err := service.GeneratePRSummary(ctx, "diff --git a/a b/a\n+++ b/a\n", []string{"org.openrewrite.java.security.Upgrade"}, "CVE-2026-9999")
	if err != nil {
		t.Fatalf("GeneratePRSummary returned error: %v", err)
	}

	if !strings.HasPrefix(summary, "⚠️ AI-assisted summary — review before merging") {
		t.Fatalf("summary missing required label")
	}
}
