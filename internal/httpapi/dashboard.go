package httpapi

import (
	"durablequeue/queue"
	"durablequeue/simulation"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
)

//go:embed web/*
var web embed.FS

func dashboard(mux *http.ServeMux, s queue.Store) {
	assets, _ := fs.Sub(web, "web")
	files := http.FileServer(http.FS(assets))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/app.js" && r.URL.Path != "/styles.css" && r.URL.Path != "/favicon.svg" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		files.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /v1/overview", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("queue")
		if q == "" {
			q = "default"
		}
		status := queue.Status(r.URL.Query().Get("status"))
		search := r.URL.Query().Get("search")
		if len(q) > 128 || strings.TrimSpace(q) == "" || strings.ContainsRune(q, '\x00') || len(search) > 256 {
			write(w, 400, map[string]string{"error": "invalid queue or search (max 128/256 bytes)"})
			return
		}
		if status != "" && status != queue.Pending && status != queue.Running && status != queue.Succeeded && status != queue.Dead {
			write(w, 400, map[string]string{"error": "unknown job status"})
			return
		}
		offset, limit := 0, 20
		for key, dest := range map[string]*int{"offset": &offset, "limit": &limit} {
			if raw := r.URL.Query().Get(key); raw != "" {
				n, e := strconv.Atoi(raw)
				if e != nil {
					write(w, 400, map[string]string{"error": "offset and limit must be integers"})
					return
				}
				*dest = n
			}
		}
		if offset < 0 || offset > 1000000 || limit < 1 || limit > 100 {
			write(w, 400, map[string]string{"error": "offset must be 0..1000000 and limit 1..100"})
			return
		}
		out, err := s.Inspect(r.Context(), q, status, search, offset, limit)
		if err != nil {
			internal(w, err)
			return
		}
		write(w, 200, out)
	})
	mux.HandleFunc("GET /v1/jobs/{id}/result", func(w http.ResponseWriter, r *http.Request) {
		result, err := s.Result(r.Context(), r.PathValue("id"))
		if errors.Is(err, queue.ErrNotFound) {
			write(w, 404, map[string]string{"error": "no committed result for this job"})
			return
		}
		if err != nil {
			internal(w, err)
			return
		}
		write(w, 200, result)
	})
	// The lab is bounded and admits one run at a time; it never touches live jobs.
	gate := make(chan struct{}, 1)
	mux.HandleFunc("POST /v1/simulations", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Seed    int64 `json:"seed"`
			Steps   int   `json:"steps"`
			Jobs    int   `json:"jobs"`
			Workers int   `json:"workers"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		d.DisallowUnknownFields()
		if err := d.Decode(&body); err != nil {
			write(w, 400, map[string]string{"error": "invalid simulation request"})
			return
		}
		if err := d.Decode(&struct{}{}); err != io.EOF {
			write(w, 400, map[string]string{"error": "expected one JSON object"})
			return
		}
		if body.Steps < 100 || body.Steps > 10000 || body.Jobs < 1 || body.Jobs > 200 || body.Workers < 1 || body.Workers > 32 || body.Seed < -9007199254740991 || body.Seed > 9007199254740991 {
			write(w, 400, map[string]string{"error": "use 100..10000 events, 1..200 jobs, 1..32 workers, and a safe integer seed"})
			return
		}
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		default:
			write(w, 429, map[string]string{"error": "a simulation is already running; try again shortly"})
			return
		}
		report, err := simulation.Run(simulation.Config{Seed: body.Seed, Steps: body.Steps, Jobs: body.Jobs, Workers: body.Workers})
		if err != nil {
			write(w, 500, map[string]string{"error": err.Error()})
			return
		}
		write(w, 200, report)
	})
}
