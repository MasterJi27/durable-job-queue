package main

import (
	"context"
	"crypto/sha256"
	"durablequeue/internal/httpapi"
	"durablequeue/queue"
	"durablequeue/queue/postgres"
	"durablequeue/simulation"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}
func printJSON(v any) error {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println(`Durable Queue — PostgreSQL-backed jobs with deterministic failure simulation

Usage: dq <command> [flags]
  simulate   Run seeded failure schedules; no database required
  migrate    Create queue tables and indexes
  enqueue    Submit an idempotent job
  worker     Run concurrent workers (echo and sha256 handlers)
  serve      Run the local HTTP API
  list       List jobs and their status
  get        Inspect a job by ID
  results    List committed results

Use dq <command> -h for flags. Database commands require DATABASE_URL.`)
		return nil
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	if args[0] == "simulate" {
		seed := fs.Int64("seed", 42, "first random seed")
		seeds := fs.Int("seeds", 1, "number of consecutive seeds")
		steps := fs.Int("steps", 10000, "events per seed")
		jobs := fs.Int("jobs", 100, "accepted jobs")
		workers := fs.Int("workers", 8, "simulated workers")
		trace := fs.Bool("trace", false, "write replay trace to stderr")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if *seeds < 1 || *seeds > 10000 {
			return errors.New("seeds must be 1..10000")
		}
		for i := 0; i < *seeds; i++ {
			c := simulation.Config{Seed: *seed + int64(i), Steps: *steps, Jobs: *jobs, Workers: *workers}
			if *trace {
				c.Trace = os.Stderr
			}
			r, err := simulation.Run(c)
			if err != nil {
				return err
			}
			if err = printJSON(r); err != nil {
				return err
			}
		}
		return nil
	}
	q := fs.String("queue", "default", "queue name")
	kind := fs.String("kind", "echo", "job handler: echo or sha256")
	key := fs.String("key", "", "idempotency key")
	payload := fs.String("payload", `{"message":"hello"}`, "JSON payload")
	attempts := fs.Int("attempts", 5, "maximum attempts")
	delay := fs.Duration("delay", 0, "delay before first attempt")
	concurrency := fs.Int("concurrency", 4, "parallel worker slots")
	lease := fs.Duration("lease", 15*time.Second, "lease duration")
	addr := fs.String("addr", "127.0.0.1:8080", "HTTP listen address")
	id := fs.String("id", "", "job ID")
	limit := fs.Int("limit", 100, "maximum jobs to list (1..1000)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	switch args[0] {
	case "migrate", "enqueue", "worker", "serve", "list", "get", "results":
	default:
		return fmt.Errorf("unknown command %q; run dq help", args[0])
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required; see .env.example")
	}
	connectCtx, connectCancel := context.WithTimeout(ctx, 10*time.Second)
	s, err := postgres.Open(connectCtx, url)
	connectCancel()
	if err != nil {
		return err
	}
	defer s.Close()
	commandCtx, commandCancel := context.WithTimeout(ctx, 30*time.Second)
	defer commandCancel()
	switch args[0] {
	case "migrate":
		return s.Migrate(commandCtx)
	case "enqueue":
		j, err := s.Enqueue(commandCtx, queue.Request{Queue: *q, Kind: *kind, Key: *key, Payload: json.RawMessage(*payload), MaxAttempts: *attempts, Delay: *delay})
		if err != nil {
			return err
		}
		return printJSON(j)
	case "list":
		if *limit < 1 || *limit > 1000 {
			return errors.New("limit must be 1..1000")
		}
		jobs, err := s.List(commandCtx, *q, *limit)
		if err != nil {
			return err
		}
		return printJSON(jobs)
	case "get":
		if *id == "" {
			return errors.New("-id is required")
		}
		j, err := s.Get(commandCtx, *id)
		if err != nil {
			return err
		}
		return printJSON(j)
	case "results":
		r, err := s.Results(commandCtx)
		if err != nil {
			return err
		}
		return printJSON(r)
	case "worker":
		owner, err := queue.NewID()
		if err != nil {
			return err
		}
		slog.Info("worker starting", "owner", owner, "concurrency", *concurrency)
		return (queue.Worker{Store: s, Queue: *q, Owner: owner, Concurrency: *concurrency, LeaseDuration: *lease, Handle: demoHandler}).Run(ctx)
	case "serve":
		server := &http.Server{Addr: *addr, Handler: httpapi.New(s, s.Ping), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
		done := make(chan error, 1)
		go func() { done <- server.ListenAndServe() }()
		slog.Info("API listening", "address", *addr)
		select {
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return server.Shutdown(shutdownCtx)
		}
	}
	return nil
}

// These handlers only compute results. Persistent writes belong in CompleteTx.
// demo_fail_until and demo_sleep_ms make retries and heartbeat recovery visible.
func demoHandler(ctx context.Context, j queue.Job) (json.RawMessage, error) {
	var opts struct {
		FailUntil int   `json:"demo_fail_until"`
		SleepMS   int64 `json:"demo_sleep_ms"`
	}
	_ = json.Unmarshal(j.Payload, &opts)
	if opts.SleepMS < 0 || opts.SleepMS > 60000 {
		return nil, errors.New("demo_sleep_ms must be 0..60000")
	}
	if opts.SleepMS > 0 {
		timer := time.NewTimer(time.Duration(opts.SleepMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if j.Attempts <= opts.FailUntil {
		return nil, fmt.Errorf("demonstration failure on attempt %d", j.Attempts)
	}
	switch j.Kind {
	case "echo":
		return json.Marshal(map[string]any{"echo": j.Payload})
	case "sha256":
		h := sha256.Sum256(j.Payload)
		return json.Marshal(map[string]string{"sha256": hex.EncodeToString(h[:])})
	default:
		return nil, fmt.Errorf("unknown handler %q", j.Kind)
	}
}
