package ring

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func isNaN(v float32) bool { return math.IsNaN(float64(v)) }

// TestWrap fills more than the ring holds and checks that old points fall off, skipped slots are
// NaN and a point older than the ring is dropped.
func TestWrap(t *testing.T) {
	s := New(15*time.Second, time.Minute) // 4 points
	for slot := int64(100); slot < 106; slot++ {
		s.Put("a", slot, float64(slot))
	}
	got := s.Range("a", 100, 106)
	want := []float64{-1, -1, 102, 103, 104, 105, -1}
	for i, w := range want {
		if (w < 0) != isNaN(got[i]) || (w >= 0 && float64(got[i]) != w) {
			t.Fatalf("range %v, want %v", got, want)
		}
	}
	s.Put("a", 108, 8) // 106 and 107 skipped
	if r := s.Range("a", 105, 108); !isNaN(r[1]) || !isNaN(r[2]) || r[3] != 8 || r[0] != 105 {
		t.Fatalf("after gap %v", r)
	}
	s.Put("a", 104, 99) // older than the ring now holds
	if r := s.Range("a", 104, 104); !isNaN(r[0]) {
		t.Fatalf("old point stored: %v", r)
	}
	s.Put("a", 200, 1) // a long gap clears everything
	if r := s.Range("a", 197, 200); !isNaN(r[0]) || !isNaN(r[1]) || !isNaN(r[2]) || r[3] != 1 {
		t.Fatalf("after long gap %v", r)
	}
	if v, slot, ok := s.Last("a"); !ok || v != 1 || slot != 200 {
		t.Fatalf("last %v %v %v", v, slot, ok)
	}
	if n := s.Prune(205); n != 1 || len(s.Keys()) != 0 {
		t.Fatalf("prune %d, keys %v", n, s.Keys())
	}
}

func TestSlots(t *testing.T) {
	s := New(15*time.Second, 7*24*time.Hour)
	if s.Len() != 40320 {
		t.Fatalf("len %d", s.Len())
	}
	tm := time.Date(2026, 11, 1, 12, 0, 7, 0, time.UTC)
	if got := s.SlotTime(s.Slot(tm)); !got.Equal(tm.Truncate(15 * time.Second)) {
		t.Fatalf("slot time %v", got)
	}
}

// TestSnapshot saves and loads a store with its extra, and refuses a snapshot made with another
// interval.
func TestSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doinspect.snap")
	s := New(15*time.Second, time.Hour)
	s.Put("pi-host/cpu", 1000, 12.5)
	s.Put("pi-host/cpu", 1002, 13.5)
	seen := map[string]time.Time{"pi-host": time.Unix(15030, 0).UTC()}
	if n, err := s.Save(path, seen); err != nil || n == 0 {
		t.Fatalf("save %d %v", n, err)
	}

	l := New(15*time.Second, time.Hour)
	extra, err := l.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if r := l.Range("pi-host/cpu", 1000, 1002); r[0] != 12.5 || !isNaN(r[1]) || r[2] != 13.5 {
		t.Fatalf("loaded %v", r)
	}
	if !extra["pi-host"].Equal(seen["pi-host"]) {
		t.Fatalf("extra %v", extra)
	}

	other := New(30*time.Second, time.Hour)
	if _, err := other.Load(path); err == nil || len(other.Keys()) != 0 {
		t.Fatalf("loaded a snapshot of another interval: %v", err)
	}
	if extra, err := New(15*time.Second, time.Hour).Load(filepath.Join(t.TempDir(), "none")); err != nil || extra != nil {
		t.Fatalf("missing file: %v %v", extra, err)
	}
}
