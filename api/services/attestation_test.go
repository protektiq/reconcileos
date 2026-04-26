package services

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeSHA256Hex(t *testing.T) {
	valid := "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	got, err := normalizeSHA256Hex(valid)
	if err != nil {
		t.Fatalf("expected valid hash, got error: %v", err)
	}
	if got != valid {
		t.Fatalf("expected normalized hash %s, got %s", valid, got)
	}

	if _, err := normalizeSHA256Hex("invalid"); err == nil {
		t.Fatalf("expected invalid hash error")
	}
}

func TestParseAttestationPage(t *testing.T) {
	page, err := ParseAttestationPage("")
	if err != nil {
		t.Fatalf("expected default page, got error: %v", err)
	}
	if page != 1 {
		t.Fatalf("expected default page 1, got %d", page)
	}

	if _, err := ParseAttestationPage("0"); err == nil {
		t.Fatalf("expected invalid page error")
	}
}

func TestBuildSLSAStatement(t *testing.T) {
	trigger := json.RawMessage(`{"event_type":"pull_request"}`)
	result := json.RawMessage(`{"ai_assisted":true}`)
	execution := executionContextRecord{
		ID:           "exec-1",
		OrgID:        "org-1",
		BotID:        "bot-1",
		TriggerEvent: trigger,
		Result:       result,
	}
	bot := botContextRecord{ID: "bot-1", Version: "1.2.3"}
	repo := repoContextRecord{ID: "repo-1", GitHubRepoFullName: "acme/repo"}
	now := time.Date(2026, time.April, 25, 19, 0, 0, 0, time.UTC)

	statement, err := buildSLSAStatement(execution, bot, repo, "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", now)
	if err != nil {
		t.Fatalf("buildSLSAStatement returned error: %v", err)
	}

	predicate := statement["predicate"].(map[string]any)
	buildDefinition := predicate["buildDefinition"].(map[string]any)
	externalParameters := buildDefinition["externalParameters"].(map[string]any)
	if externalParameters["trigger_event_type"] != "pull_request" {
		t.Fatalf("expected trigger_event_type pull_request, got %v", externalParameters["trigger_event_type"])
	}
	if externalParameters["ai_assisted"] != true {
		t.Fatalf("expected ai_assisted true, got %v", externalParameters["ai_assisted"])
	}
}
