package httpapi

import (
	"context"
	"durablequeue/queue"
	"durablequeue/queue/memory"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPI(t *testing.T) {
	h := New(memory.New(time.Now), func(context.Context) error { return nil })
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(method, path, strings.NewReader(body)))
		return r
	}
	body := `{"kind":"echo","idempotency_key":"a","payload":{"hello":"world"}}`
	a := call("POST", "/v1/jobs", body)
	if a.Code != 202 {
		t.Fatal(a.Code, a.Body.String())
	}
	var j queue.Job
	if e := json.Unmarshal(a.Body.Bytes(), &j); e != nil {
		t.Fatal(e)
	}
	b := call("POST", "/v1/jobs", body)
	var duplicate queue.Job
	_ = json.Unmarshal(b.Body.Bytes(), &duplicate)
	if b.Code != 202 || duplicate.ID != j.ID {
		t.Fatal("API idempotency failed")
	}
	if r := call("POST", "/v1/jobs", strings.Replace(body, "world", "different", 1)); r.Code != 409 {
		t.Fatal("API conflict missing")
	}
	for _, bad := range []string{`{}`, `{"kind":"echo","unknown":1}`, body + body, `{"kind":"echo","idempotency_key":"x","payload":{},"delay_ms":9223372036854775807}`} {
		if r := call("POST", "/v1/jobs", bad); r.Code != 400 {
			t.Fatalf("invalid request accepted: %s", bad)
		}
	}
	for path, code := range map[string]int{"/healthz": 200, "/v1/jobs": 200, "/v1/jobs/" + j.ID: 200, "/v1/jobs/missing": 404, "/v1/jobs?limit=-1": 400} {
		if r := call(http.MethodGet, path, ""); r.Code != code {
			t.Fatalf("%s: got %d want %d", path, r.Code, code)
		}
	}
}
