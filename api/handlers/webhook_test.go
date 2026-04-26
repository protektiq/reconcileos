package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestValidateGitHubWebhookSignature(t *testing.T) {
	payload := []byte(`{"installation":{"id":123}}`)
	secret := "top-secret"

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name      string
		signature string
		wantErr   bool
	}{
		{name: "valid signature", signature: validSig, wantErr: false},
		{name: "missing prefix", signature: hex.EncodeToString(mac.Sum(nil)), wantErr: true},
		{name: "bad hex", signature: "sha256=ZZZ", wantErr: true},
		{name: "mismatch", signature: "sha256=" + hex.EncodeToString([]byte("mismatch")), wantErr: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateGitHubWebhookSignature(payload, testCase.signature, secret)
			if testCase.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestExtractInstallationID(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{name: "valid payload", payload: []byte(`{"installation":{"id":42}}`), wantErr: false},
		{name: "missing installation", payload: []byte(`{"foo":"bar"}`), wantErr: true},
		{name: "invalid json", payload: []byte(`{`), wantErr: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			installationID, err := extractInstallationID(testCase.payload)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if installationID <= 0 {
				t.Fatalf("expected positive installation ID, got %d", installationID)
			}
		})
	}
}
