package control

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Handler serves the local control API. All routes require the bearer token.
func Handler(token string, b Backend) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.Runtime())
	})
	mux.HandleFunc("GET /v1/dashboard", func(w http.ResponseWriter, r *http.Request) {
		tail := queryInt(r, "tail", 400)
		writeJSON(w, http.StatusOK, map[string]any{
			"runtime": b.Runtime(),
			"logs":    b.Logs("", tail),
		})
	})
	mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		tail := queryInt(r, "tail", 200)
		writeJSON(w, http.StatusOK, map[string]any{
			"service": service,
			"lines":   b.Logs(service, tail),
		})
	})
	mux.HandleFunc("POST /v1/services/{name}/restart", func(w http.ResponseWriter, r *http.Request) {
		if err := b.Restart(r.Context(), r.PathValue("name")); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/share", func(w http.ResponseWriter, r *http.Request) {
		url, err := b.Share(r.Context())
		rt := b.Runtime()
		body := map[string]string{
			"url":   url,
			"hint":  rt.ShareHint,
			"error": rt.ShareError,
		}
		if err != nil && url == "" {
			body["error"] = err.Error()
			writeJSON(w, http.StatusBadGateway, body)
			return
		}
		writeJSON(w, http.StatusOK, body)
	})
	mux.HandleFunc("POST /v1/shutdown", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		b.Stop()
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func authorized(r *http.Request, token string) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return got == token && token != ""
}

func queryInt(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
