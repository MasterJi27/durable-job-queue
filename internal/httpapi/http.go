package httpapi

import (
	"context"
	"durablequeue/queue"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func New(s queue.Store, ping func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	dashboard(mux, s)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ping(ctx); err != nil {
			write(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		write(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Queue       string          `json:"queue"`
			Kind        string          `json:"kind"`
			Key         string          `json:"idempotency_key"`
			Payload     json.RawMessage `json:"payload"`
			MaxAttempts int             `json:"max_attempts"`
			DelayMS     int64           `json:"delay_ms"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		d.DisallowUnknownFields()
		if err := d.Decode(&body); err != nil {
			write(w, 400, map[string]string{"error": "invalid JSON request or body exceeds 1 MiB"})
			return
		}
		if err := d.Decode(&struct{}{}); err != io.EOF {
			write(w, 400, map[string]string{"error": "expected one JSON object"})
			return
		}
		if body.DelayMS < 0 || body.DelayMS > int64((30*24*time.Hour)/time.Millisecond) {
			write(w, 400, map[string]string{"error": "delay_ms must be 0..2592000000"})
			return
		}
		req := queue.Request{Queue: body.Queue, Kind: body.Kind, Key: body.Key, Payload: body.Payload, MaxAttempts: body.MaxAttempts, Delay: time.Duration(body.DelayMS) * time.Millisecond}
		if _, _, err := req.Normalize(); err != nil {
			write(w, 400, map[string]string{"error": err.Error()})
			return
		}
		j, err := s.Enqueue(r.Context(), req)
		if errors.Is(err, queue.ErrConflict) {
			write(w, 409, map[string]string{"error": err.Error()})
			return
		}
		if err != nil {
			internal(w, err)
			return
		}
		w.Header().Set("Location", "/v1/jobs/"+j.ID)
		write(w, http.StatusAccepted, j)
	})
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("queue")
		if q == "" {
			q = "default"
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 1000 {
				write(w, 400, map[string]string{"error": "limit must be 1..1000"})
				return
			}
			limit = n
		}
		jobs, err := s.List(r.Context(), q, limit)
		if err != nil {
			internal(w, err)
			return
		}
		write(w, 200, jobs)
	})
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		j, err := s.Get(r.Context(), r.PathValue("id"))
		if errors.Is(err, queue.ErrNotFound) {
			write(w, 404, map[string]string{"error": "job not found"})
			return
		}
		if err != nil {
			internal(w, err)
			return
		}
		write(w, 200, j)
	})
	timed := http.TimeoutHandler(mux, 10*time.Second, `{"error":"request timed out"}`)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/healthz" {
			w.Header().Set("Cache-Control", "no-store")
		}
		timed.ServeHTTP(w, r)
	})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func internal(w http.ResponseWriter, err error) {
	slog.Error("API database operation failed", "error", err)
	write(w, 500, map[string]string{"error": "database operation failed"})
}
