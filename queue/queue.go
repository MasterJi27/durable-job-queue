// Package queue defines the queue protocol shared by PostgreSQL and the simulator.
package queue

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	ErrEmpty     = errors.New("no eligible job")
	ErrNotFound  = errors.New("job not found")
	ErrLeaseLost = errors.New("lease expired or superseded")
	ErrConflict  = errors.New("idempotency key reused with a different kind or payload")
)

type Status string

const (
	Pending   Status = "pending"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Dead      Status = "dead"
)

type Job struct {
	ID          string          `json:"id"`
	Queue       string          `json:"queue"`
	Kind        string          `json:"kind"`
	Key         string          `json:"idempotency_key"`
	Payload     json.RawMessage `json:"payload"`
	Fingerprint string          `json:"-"`
	Status      Status          `json:"status"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	Version     int64           `json:"lease_version"`
	Owner       string          `json:"owner,omitempty"`
	AvailableAt time.Time       `json:"available_at"`
	LeaseUntil  time.Time       `json:"lease_until"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	LastError   string          `json:"last_error,omitempty"`
}

type Request struct {
	Queue       string          `json:"queue"`
	Kind        string          `json:"kind"`
	Key         string          `json:"idempotency_key"`
	Payload     json.RawMessage `json:"payload"`
	MaxAttempts int             `json:"max_attempts"`
	Delay       time.Duration   `json:"-"`
}

func (r Request) Normalize() (Request, string, error) {
	if r.Queue == "" {
		r.Queue = "default"
	}
	if r.MaxAttempts == 0 {
		r.MaxAttempts = 5
	}
	if strings.TrimSpace(r.Queue) == "" || len(r.Queue) > 128 || strings.TrimSpace(r.Kind) == "" || len(r.Kind) > 128 || strings.TrimSpace(r.Key) == "" || len(r.Key) > 256 || strings.ContainsRune(r.Queue+r.Kind+r.Key, '\x00') {
		return r, "", errors.New("queue/kind must be 1..128 bytes and idempotency_key 1..256 bytes")
	}
	if r.MaxAttempts < 1 || r.MaxAttempts > 100 || r.Delay < 0 || r.Delay > 30*24*time.Hour {
		return r, "", errors.New("max_attempts must be 1..100 and delay 0..30 days")
	}
	if len(r.Payload) == 0 || len(r.Payload) > 1<<20 || !json.Valid(r.Payload) {
		return r, "", errors.New("payload must be valid JSON, at most 1 MiB")
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(r.Payload))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return r, "", err
	}
	r.Payload, _ = json.Marshal(value)
	r.Delay = r.Delay.Truncate(time.Microsecond)
	h := sha256.Sum256(append([]byte(r.Kind+"\n"), r.Payload...))
	return r, hex.EncodeToString(h[:]), nil
}

type Lease struct {
	JobID   string `json:"job_id"`
	Owner   string `json:"owner"`
	Version int64  `json:"version"`
}

func (j Job) Lease() Lease   { return Lease{j.ID, j.Owner, j.Version} }
func (j Job) Clone() Job     { j.Payload = bytes.Clone(j.Payload); return j }
func (j Job) Terminal() bool { return j.Status == Succeeded || j.Status == Dead }

type Result struct {
	JobID       string          `json:"job_id"`
	Value       json.RawMessage `json:"value"`
	CommittedAt time.Time       `json:"committed_at"`
}

// Inspection is a coherent queue snapshot used by the dashboard. Counts cover
// the entire queue, while Jobs contains one page of the matching selection.
type Stats struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Succeeded int `json:"succeeded"`
	Dead      int `json:"dead"`
}
type Inspection struct {
	Queue      string    `json:"queue"`
	Stats      Stats     `json:"stats"`
	Jobs       []Job     `json:"jobs"`
	Filtered   int       `json:"filtered"`
	Offset     int       `json:"offset"`
	Limit      int       `json:"limit"`
	ServerTime time.Time `json:"server_time"`
}

type Store interface {
	Enqueue(context.Context, Request) (Job, error)
	Claim(context.Context, string, string, time.Duration) (Job, error)
	Extend(context.Context, Lease, time.Duration) error
	Complete(context.Context, Lease, json.RawMessage) error
	Fail(context.Context, Lease, string) error
	Get(context.Context, string) (Job, error)
	List(context.Context, string, int) ([]Job, error)
	Results(context.Context) ([]Result, error)
	Result(context.Context, string) (Result, error)
	Inspect(context.Context, string, Status, string, int, int) (Inspection, error)
}

func NewID() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", fmt.Errorf("job ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func ValidateClaim(q, owner string, duration time.Duration) error {
	if strings.TrimSpace(q) == "" || len(q) > 128 || strings.TrimSpace(owner) == "" || len(owner) > 256 || strings.ContainsRune(q+owner, '\x00') {
		return errors.New("queue and owner are required (max 128/256 bytes)")
	}
	return ValidateDuration(duration)
}

func ValidateDuration(d time.Duration) error {
	if d < time.Millisecond || d > 24*time.Hour {
		return errors.New("lease duration must be 1ms..24h")
	}
	return nil
}

func Owns(j Job, l Lease) bool {
	return j.ID == l.JobID && j.Owner == l.Owner && j.Version == l.Version
}
func CheckLease(j Job, l Lease, now time.Time) error {
	if !Owns(j, l) || j.Status != Running || !now.Before(j.LeaseUntil) {
		return ErrLeaseLost
	}
	return nil
}

// Backoff is bounded exponential backoff with reproducible per-job jitter.
func Backoff(id string, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 10 {
		attempt = 10
	}
	d := time.Second * time.Duration(1<<uint(attempt-1))
	if d > time.Minute {
		d = time.Minute
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", id, attempt)))
	return d + time.Duration(h[0])*d/1024
}

// Acquire and Reject are also used by the durable store while holding row locks.
func Acquire(j Job, owner string, now time.Time, d time.Duration) Job {
	j.Status = Running
	j.Owner = owner
	j.Version++
	j.Attempts++
	j.LeaseUntil = now.Add(d.Truncate(time.Microsecond))
	j.UpdatedAt = now
	return j
}
func Reject(j Job, now time.Time, reason string) Job {
	j.LastError = strings.ToValidUTF8(reason, "?")
	if len(j.LastError) > 2048 {
		j.LastError = strings.ToValidUTF8(j.LastError[:2048], "?")
	}
	j.UpdatedAt = now
	j.LeaseUntil = time.Time{}
	if j.Attempts >= j.MaxAttempts {
		j.Status = Dead
	} else {
		j.Status = Pending
		j.AvailableAt = now.Add(Backoff(j.ID, j.Attempts))
	}
	return j
}
