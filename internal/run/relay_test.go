package run

import "testing"

func TestResolveRelay(t *testing.T) {
	if got := resolveRelay("", "", ""); got != "https://lnch.sh" {
		t.Fatalf("default %q", got)
	}
	if got := resolveRelay("off", "https://example.test", "https://other.test"); got != "" {
		t.Fatalf("flag off %q", got)
	}
	if got := resolveRelay("", "http://127.0.0.1:9090", "https://other.test"); got != "http://127.0.0.1:9090" {
		t.Fatalf("file %q", got)
	}
	if got := resolveRelay("", "", "none"); got != "" {
		t.Fatalf("env off %q", got)
	}
	if got := resolveRelay("", "", " https://relay.example "); got != "https://relay.example" {
		t.Fatalf("env %q", got)
	}
}
