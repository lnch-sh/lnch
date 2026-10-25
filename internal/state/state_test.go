package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestProjectIDStable(t *testing.T) {
	a := ProjectID(filepath.Join("C:", "Projects", "Demo"))
	b := ProjectID(filepath.Join("C:", "Projects", "Demo"))
	if a != b || len(a) != 16 {
		t.Fatalf("id %q %q", a, b)
	}
}

func TestSaveLoadConcurrent(t *testing.T) {
	dir := t.TempDir()
	rt := Runtime{Root: dir, Project: "demo", Token: "secret", Control: "http://127.0.0.1:1", PID: os.Getpid()}
	if err := Save(rt); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			rt.PID = i + 1
			if err := Save(rt); err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
		}
	}()
	for i := 0; i < 40; i++ {
		got, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got.Token != "secret" || got.Control == "" {
			t.Fatalf("load %d: %+v", i, got)
		}
	}
	wg.Wait()
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
}
