package discover

import "testing"

func TestPort(t *testing.T) {
	cases := []struct {
		line string
		want int
	}{
		{"Local:   http://localhost:5173/", 5173},
		{"- Local: http://127.0.0.1:3000", 3000},
		{"listening on http://0.0.0.0:8080", 8080},
		{"listening on :8080", 8080},
		{"Uvicorn running on http://127.0.0.1:8000 (Press CTRL+C to quit)", 8000},
		{"\x1b[32mready on http://localhost:4173\x1b[0m", 4173},
		{"compiled successfully", 0},
		{"pid 41233", 0},
	}
	for _, tc := range cases {
		if got := Port(tc.line); got != tc.want {
			t.Errorf("Port(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}
}
