package tunnel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientAgainstRelay(t *testing.T) {
	base := os.Getenv("LNCH_RELAY_TEST_URL")
	if base == "" {
		t.Skip("set LNCH_RELAY_TEST_URL to check a running lnch relay")
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hello" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "hello "+r.URL.Query().Get("x"))
	}))
	defer origin.Close()

	client := &Client{Base: base}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg, err := client.Open(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reg.URL, reg.ID) || reg.Token == "" {
		t.Fatalf("registration %+v", reg)
	}
	go func() { _ = client.Serve(ctx, reg, origin.URL) }()

	targets := []string{reg.DirectURL}
	if reg.URL != reg.DirectURL {
		targets = append(targets, reg.URL)
	}
	for _, target := range targets {
		body := waitHello(t, strings.TrimRight(target, "/")+"/hello?x=lnch")
		if body != "hello lnch" {
			t.Fatalf("%s body %q", target, body)
		}
	}
}

func waitHello(t *testing.T, rawURL string) string {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var last string
	for {
		resp, err := http.Get(rawURL)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			last = string(b)
			if resp.StatusCode == http.StatusOK {
				return last
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("tunnel did not serve %s: %v %q", rawURL, err, last)
		}
		time.Sleep(40 * time.Millisecond)
	}
}
