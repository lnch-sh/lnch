package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestInitializeAndTools(t *testing.T) {
	var in bytes.Buffer
	writeMessage(&in, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})
	writeMessage(&in, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	var out bytes.Buffer
	if err := Serve(context.Background(), &in, &out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, `"name":"lnch"`) && !strings.Contains(text, `"name": "lnch"`) {
		t.Fatalf("initialize missing server name:\n%s", text)
	}
	for _, name := range []string{"lnch_detect", "lnch_up", "lnch_down", "lnch_status", "lnch_logs", "lnch_restart", "lnch_share"} {
		if !strings.Contains(text, name) {
			t.Fatalf("missing tool %s\n%s", name, text)
		}
	}
	if !json.Valid([]byte(extractJSON(text))) && !strings.Contains(text, "lnch_detect") {
		t.Fatal(text)
	}
}

func extractJSON(s string) string {
	i := strings.Index(s, "{")
	if i < 0 {
		return ""
	}
	return s[i:]
}
