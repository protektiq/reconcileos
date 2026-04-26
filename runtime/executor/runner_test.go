package executor

import "testing"

func TestParseJSONOutput(t *testing.T) {
	content := []byte(`{"requires_review": true, "summary": "ok"}`)
	decoded, ok := parseJSONOutput(content)
	if !ok {
		t.Fatal("expected json output to parse")
	}
	if decoded["summary"] != "ok" {
		t.Fatalf("unexpected summary value: %v", decoded["summary"])
	}
}

func TestParseJSONOutputInvalid(t *testing.T) {
	_, ok := parseJSONOutput([]byte("not-json"))
	if ok {
		t.Fatal("expected invalid json to fail parsing")
	}
}

func TestTruncateBytes(t *testing.T) {
	content := []byte("123456")
	truncated := truncateBytes(content, 3)
	if string(truncated) != "123" {
		t.Fatalf("unexpected truncate result: %s", string(truncated))
	}
}
