package plan

import "testing"

func TestExpand(t *testing.T) {
	ports := map[string]int{"db": 5432, "api": 8080}
	got, warns := Expand("listen ${port} db ${db.port} $PORT", 3000, ports)
	if got != "listen 3000 db 5432 3000" {
		t.Fatalf("got %q", got)
	}
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}

	got, warns = Expand("x ${missing.port} ${port}", 0, nil)
	if got != "x ${missing.port} ${port}" {
		t.Fatalf("got %q", got)
	}
	if len(warns) != 2 {
		t.Fatalf("warnings: %v", warns)
	}
}

func TestNeedsPort(t *testing.T) {
	if !NeedsPort(Service{Command: "uvicorn --port ${port}"}) {
		t.Fatal("expected port token to request a port")
	}
	if NeedsPort(Service{Command: "npm run dev"}) {
		t.Fatal("plain command should not reserve a port")
	}
	if !NeedsPort(Service{Port: 5173}) {
		t.Fatal("explicit port should reserve")
	}
}
