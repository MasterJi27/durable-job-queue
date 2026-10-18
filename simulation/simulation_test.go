package simulation

import (
	"bytes"
	"reflect"
	"testing"
)

func TestFailureSchedules(t *testing.T) {
	stale := 0
	crashes := 0
	lostAcks := 0
	for seed := int64(0); seed < 200; seed++ {
		r, err := Run(Config{Seed: seed, Steps: 1000, Jobs: 30, Workers: 5})
		if err != nil {
			t.Fatal(err)
		}
		if r.Accepted != r.Succeeded+r.Dead {
			t.Fatalf("unaccounted work: %+v", r)
		}
		stale += r.StaleRejections
		crashes += r.Crashes
		lostAcks += r.LostAcknowledgements
		if r.ClockJumps == 0 {
			t.Fatalf("fault schedule did not exercise failures: %+v", r)
		}
	}
	if stale == 0 || crashes == 0 || lostAcks == 0 {
		t.Fatal("schedules did not exercise stale fencing, active-worker crashes, and lost acknowledgements")
	}
}
func TestReplay(t *testing.T) {
	var a, b bytes.Buffer
	c := Config{Seed: 42, Steps: 2000, Jobs: 40, Workers: 6, Trace: &a}
	ra, err := Run(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Trace = &b
	rb, err := Run(c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ra, rb) || !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("same seed did not replay byte-for-byte")
	}
}
func FuzzSchedules(f *testing.F) {
	for _, s := range []int64{0, 1, 42, 2026, -1} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		_, err := Run(Config{Seed: seed, Steps: 300, Jobs: 12, Workers: 3})
		if err != nil {
			t.Fatal(err)
		}
	})
}
