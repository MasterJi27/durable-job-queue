package httpapi

import (
	"context"
	"durablequeue/queue"
	"durablequeue/queue/memory"
	"durablequeue/simulation"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDashboard(t *testing.T) {
	s := memory.New(time.Now)
	h := New(s, func(context.Context) error { return nil })
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(method, path, strings.NewReader(body)))
		return r
	}
	for _, path := range []string{"/", "/styles.css", "/app.js", "/favicon.svg"} {
		r := call("GET", path, "")
		if r.Code != 200 || r.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("asset %s: %d", path, r.Code)
		}
	}
	if r := call("GET", "/", " "); !strings.Contains(r.Body.String(), "Queue console") {
		t.Fatal("dashboard missing")
	}
	for _, path := range []string{"/schema.sql", "/missing.js", "/v1/unknown"} {
		if r := call("GET", path, ""); r.Code != 404 {
			t.Fatalf("unexpected asset exposed: %s", path)
		}
	}
	for _, path := range []string{"/v1/overview?limit=0", "/v1/overview?limit=101", "/v1/overview?status=nope", "/v1/overview?offset=-1", "/v1/overview?limit=bad"} {
		if r := call("GET", path, ""); r.Code != 400 {
			t.Fatalf("invalid query accepted: %s", path)
		}
	}
	job, err := s.Enqueue(context.Background(), queue.Request{Kind: "echo", Key: "gui", Payload: json.RawMessage(`{"a":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	r := call("GET", "/v1/overview?limit=1", "")
	var overview queue.Inspection
	if err = json.Unmarshal(r.Body.Bytes(), &overview); err != nil || r.Code != 200 || overview.Stats.Pending != 1 {
		t.Fatalf("overview: %s %v", r.Body.String(), err)
	}
	if r = call("GET", "/v1/jobs/"+job.ID+"/result", ""); r.Code != 404 {
		t.Fatal("uncommitted result exposed")
	}
	job, err = s.Claim(context.Background(), "default", "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(context.Background(), job.Lease(), json.RawMessage(`true`)); err != nil {
		t.Fatal(err)
	}
	if r = call("GET", "/v1/jobs/"+job.ID+"/result", ""); r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("result endpoint: %s", r.Body.String())
	}
	body := `{"seed":42,"steps":500,"jobs":12,"workers":3}`
	a := call("POST", "/v1/simulations", body)
	b := call("POST", "/v1/simulations", body)
	if a.Code != 200 || a.Body.String() != b.Body.String() {
		t.Fatalf("simulation endpoint not reproducible: %s", a.Body.String())
	}
	var report simulation.Report
	_ = json.Unmarshal(a.Body.Bytes(), &report)
	if report.Accepted != report.Succeeded+report.Dead || report.InvariantChecks != 501 {
		t.Fatal("incomplete simulation")
	}
	for _, body := range []string{`{}`, `{"seed":42,"steps":10001,"jobs":1,"workers":1}`, `{"seed":42,"steps":100,"jobs":201,"workers":1}`, body + body} {
		if r = call("POST", "/v1/simulations", body); r.Code != 400 {
			t.Fatalf("unbounded simulation accepted: %s", body)
		}
	}
	view, err := s.Inspect(context.Background(), "default", "", "", 0, 20)
	if err != nil || view.Stats.Total != 1 || view.Stats.Succeeded != 1 {
		t.Fatal("simulation modified live queue")
	}
}
