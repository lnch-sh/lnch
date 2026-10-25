package supervisor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"lnch.sh/internal/plan"
	"lnch.sh/internal/state"
)

func TestMain(m *testing.M) {
	if os.Getenv("LNCH_HELPER") == "http" {
		runHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runHelper() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Printf("listening on http://%s\n", ln.Addr().String())
	_ = os.Stdout.Sync()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	_ = http.Serve(ln, mux)
}

func TestDependencyOrderAndRestart(t *testing.T) {
	dir := t.TempDir()
	helper := plan.Service{
		Argv:     []string{os.Args[0]},
		Dir:      dir,
		Env:      map[string]string{"LNCH_HELPER": "http"},
		Protocol: "http",
		Health:   "http://127.0.0.1:${port}/health",
	}
	db := helper
	db.Name = "db"
	api := helper
	api.Name = "api"
	api.DependsOn = []string{"db"}
	sup, err := Start(Options{Plan: plan.Plan{
		Name:     "demo",
		Root:     dir,
		HTTPS:    false,
		Services: []plan.Service{db, api},
	}})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- sup.Run(context.Background()) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = sup.Shutdown(ctx)
		<-errCh
	})

	select {
	case <-sup.Ready():
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("control server did not start")
	}

	waitHealthy(t, sup, "db")
	waitHealthy(t, sup, "api")
	rt := sup.Runtime()
	dbS, apiS := find(rt, "db"), find(rt, "api")
	if apiS.Started.Before(dbS.HealthyAt) {
		t.Fatalf("api started %s before db became healthy %s", apiS.Started, dbS.HealthyAt)
	}
	if dbS.URL == "" || apiS.Port == 0 {
		t.Fatalf("missing urls: %+v", rt.Services)
	}

	old := dbS.PID
	if err := sup.Restart(context.Background(), "db"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		cur := find(sup.Runtime(), "db")
		if cur.Status == state.Healthy && cur.PID != 0 && cur.PID != old {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restart did not become healthy: %+v logs=%v", cur, sup.Logs("db", 20))
		}
		time.Sleep(50 * time.Millisecond)
	}

	disk, err := state.Load(dir)
	if err != nil || disk.Token == "" || disk.Control == "" {
		t.Fatalf("state: %v %+v", err, disk)
	}
}

func waitHealthy(t *testing.T, sup *Supervisor, name string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		cur := find(sup.Runtime(), name)
		if cur.Status == state.Healthy {
			return
		}
		if cur.Status == state.Exited || cur.Status == state.Unhealthy {
			t.Fatalf("%s %s: %s\nlogs: %v", name, cur.Status, cur.LastLine, sup.Logs(name, 30))
		}
		time.Sleep(40 * time.Millisecond)
	}
	cur := find(sup.Runtime(), name)
	t.Fatalf("%s not healthy: %+v logs=%v", name, cur, sup.Logs(name, 30))
}

func find(rt state.Runtime, name string) state.Service {
	for _, s := range rt.Services {
		if s.Name == name {
			return s
		}
	}
	return state.Service{}
}
