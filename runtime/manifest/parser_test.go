package manifest

import "testing"

func TestParseValidManifest(t *testing.T) {
	input := []byte(`
name: pr-bot
version: 1.0.0
description: reconciles PR issues
author: reconcileos
triggers:
  - push
binary: /usr/local/bin/pr-bot
pricing_tier: free
price_per_execution: 0
max_timeout_seconds: 120
allowed_egress:
  - api.github.com
`)

	parsed, err := Parse(input)
	if err != nil {
		t.Fatalf("expected valid manifest, got error: %v", err)
	}
	if parsed.Name != "pr-bot" {
		t.Fatalf("unexpected name: %s", parsed.Name)
	}
}

func TestParseRejectsRelativeBinary(t *testing.T) {
	input := []byte(`
name: pr-bot
version: 1.0.0
author: reconcileos
triggers:
  - push
binary: ./bin/pr-bot
pricing_tier: free
price_per_execution: 0
max_timeout_seconds: 120
`)

	_, err := Parse(input)
	if err == nil {
		t.Fatal("expected parse to fail for relative binary")
	}
}

func TestParseRejectsInvalidTimeout(t *testing.T) {
	input := []byte(`
name: pr-bot
version: 1.0.0
author: reconcileos
triggers:
  - push
binary: /usr/local/bin/pr-bot
pricing_tier: free
price_per_execution: 0
max_timeout_seconds: 1000
`)

	_, err := Parse(input)
	if err == nil {
		t.Fatal("expected parse to fail for timeout > 600")
	}
}
