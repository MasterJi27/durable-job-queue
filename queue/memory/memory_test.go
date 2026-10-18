package memory_test

import (
	"durablequeue/internal/queuetest"
	"durablequeue/queue/memory"
	"testing"
	"time"
)

func TestContract(t *testing.T) {
	queuetest.Contract(t, func(t *testing.T) queuetest.Fixture {
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		return queuetest.Fixture{Store: memory.New(func() time.Time { return now }), Advance: func(d time.Duration) { now = now.Add(d) }}
	})
}
