package services

import (
	"strings"
	"testing"
)

func TestNormalizeRemediationPRInput_Validation(t *testing.T) {
	t.Helper()

	_, err := normalizeRemediationPRInput(RemediationPRInput{})
	if err == nil {
		t.Fatalf("expected validation error for empty input")
	}

	valid := RemediationPRInput{
		OrgID:      "e9f7c00b-d6f7-4cc7-8ddf-07f8d9834460",
		Repo:       "acme/service",
		BranchName: "reconcileos/cve-2026-12345",
		Diff:       "diff --git a/go.mod b/go.mod\n+++ b/go.mod\n",
		PRTitle:    "fix: remediate CVE-2026-12345",
		Summary:    "⚠️ AI-assisted summary — review before merging\n\nPatched vulnerable dependency.",
		Attestation: AttestationReceipt{
			RekorLogIndex: 1234,
			RekorURL:      "https://rekor.sigstore.dev/api/v1/log/entries/example",
			BotName:       "claude-triage-bot",
			BotVersion:    "1.0.0",
		},
	}

	if _, err := normalizeRemediationPRInput(valid); err != nil {
		t.Fatalf("expected valid remediation input, got error: %v", err)
	}
}

func TestBuildRemediationPRBody_Template(t *testing.T) {
	t.Helper()

	body := buildRemediationPRBody(
		"⚠️ AI-assisted summary — review before merging\n\nPatched vulnerable dependency.",
		AttestationReceipt{
			RekorLogIndex: 77,
			RekorURL:      "https://rekor.sigstore.dev/api/v1/log/entries/abc",
			BotName:       "claude-triage-bot",
			BotVersion:    "1.2.3",
		},
		[]string{"go.mod", "go.sum"},
	)

	requiredParts := []string{
		"## ReconcileOS Automated Remediation",
		"### Attestation",
		"Rekor log index: 77 | [View proof](https://rekor.sigstore.dev/api/v1/log/entries/abc)",
		"Bot: claude-triage-bot@1.2.3",
		"⚠️ AI-assisted — review before merging",
		"### Changes",
		"- go.mod",
		"- go.sum",
	}
	for _, part := range requiredParts {
		if !strings.Contains(body, part) {
			t.Fatalf("PR body missing required part: %s", part)
		}
	}
}

func TestExtractChangedFilesFromDiff(t *testing.T) {
	t.Helper()

	diff := strings.Join([]string{
		"diff --git a/go.mod b/go.mod",
		"+++ b/go.mod",
		"diff --git a/go.sum b/go.sum",
		"+++ b/go.sum",
		"diff --git a/go.mod b/go.mod",
		"+++ b/go.mod",
	}, "\n")

	changed := extractChangedFilesFromDiff(diff)
	if len(changed) != 2 {
		t.Fatalf("expected 2 unique changed files, got %d", len(changed))
	}
	if changed[0] != "go.mod" || changed[1] != "go.sum" {
		t.Fatalf("unexpected changed files: %#v", changed)
	}
}
