package tunnel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	rl := &Relay{}
	relaySrv := httptest.NewServer(rl)
	defer relaySrv.Close()
	rl.SetAddr(relaySrv.Listener.Addr().String())

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hello" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "hello "+r.URL.Query().Get("x"))
	}))
	defer origin.Close()

	client := &Client{Base: relaySrv.URL}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg, err := client.Open(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = client.Serve(ctx, reg, origin.URL) }()

	deadline := time.Now().Add(5 * time.Second)
	var body string
	for {
		resp, err := http.Get(relaySrv.URL + "/t/" + reg.ID + "/hello?x=lnch")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				body = string(b)
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("tunnel did not serve hello: %v %q", err, body)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if body != "hello lnch" {
		t.Fatalf("body %q", body)
	}
}
