package state

import (
	"path/filepath"
	"testing"
)

func TestProjectIDStable(t *testing.T) {
	a := ProjectID(filepath.Join("C:", "Projects", "Demo"))
	b := ProjectID(filepath.Join("C:", "Projects", "Demo"))
	if a != b || len(a) != 16 {
		t.Fatalf("id %q %q", a, b)
	}
}
