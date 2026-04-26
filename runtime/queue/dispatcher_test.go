package queue

import (
	"testing"

	"github.com/reconcileos/reconcileos/runtime/manifest"
)

func TestEventMatches(t *testing.T) {
	data := manifest.BotManifest{
		Triggers: []string{"push", "installation"},
	}
	if !eventMatches(data, "push") {
		t.Fatal("expected event to match push trigger")
	}
	if eventMatches(data, "pull_request") {
		t.Fatal("expected event not to match pull_request")
	}
}
