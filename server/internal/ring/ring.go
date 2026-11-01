// Package ring keeps every series as a fixed ring of float32 points, one per interval, for the
// time doinspect keeps (D1): 40,320 points for 7 days at 15 s, 160 kB per series. A point's time
// is its slot number times the interval; a missing point is NaN.
package ring

import (
	"math"
	"sort"
	"sync"
	"time"
)

var nan32 = float32(math.NaN())

// Store holds the rings of all series. It is safe for use by several goroutines.
type Store struct {
	interval time.Duration
	n        int64
	mu       sync.RWMutex
	series   map[string]*ring
}

type ring struct {
	head int64 // slot of the newest point
	vals []float32
}

// New makes a store that keeps keep worth of points at one per interval.
func New(interval, keep time.Duration) *Store {
	return &Store{interval: interval, n: int64(keep / interval), series: map[string]*ring{}}
}

func (s *Store) Interval() time.Duration { return s.interval }
func (s *Store) Len() int                { return int(s.n) }

// Slot is the number of the interval t falls in.
func (s *Store) Slot(t time.Time) int64 { return t.Unix() / int64(s.interval/time.Second) }

// SlotTime is the start of slot n.
func (s *Store) SlotTime(n int64) time.Time {
	return time.Unix(n*int64(s.interval/time.Second), 0).UTC()
}

// Put stores v as the point of series key at slot. Slots skipped since the newest point become
// NaN; a slot older than the ring is dropped.
func (s *Store) Put(key string, slot int64, v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.series[key]
	if r == nil {
		r = &ring{head: slot, vals: make([]float32, s.n)}
		for i := range r.vals {
			r.vals[i] = nan32
		}
		s.series[key] = r
	}
	if slot > r.head {
		for n := r.head + 1; n < slot && n <= r.head+s.n; n++ {
			r.vals[mod(n, s.n)] = nan32
		}
		r.head = slot
	}
	if slot <= r.head-s.n {
		return
	}
	r.vals[mod(slot, s.n)] = float32(v)
}

// Range returns the points of series key from slot from to slot to, both included, NaN where
// there is none. An unknown series gives nil.
func (s *Store) Range(key string, from, to int64) []float32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.series[key]
	if r == nil || to < from {
		return nil
	}
	out := make([]float32, to-from+1)
	for n := from; n <= to; n++ {
		if n > r.head || n <= r.head-s.n {
			out[n-from] = nan32
		} else {
			out[n-from] = r.vals[mod(n, s.n)]
		}
	}
	return out
}

// Last returns the newest point of series key and its slot; ok is false for an unknown series or
// a newest point that is NaN.
func (s *Store) Last(key string) (v float64, slot int64, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.series[key]
	if r == nil {
		return 0, 0, false
	}
	x := r.vals[mod(r.head, s.n)]
	return float64(x), r.head, !math.IsNaN(float64(x))
}

// Keys lists the series, sorted.
func (s *Store) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.series))
	for k := range s.series {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Prune drops series whose newest point is older than the ring, such as a container removed
// a week ago.
func (s *Store) Prune(now int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, r := range s.series {
		if r.head <= now-s.n {
			delete(s.series, k)
			n++
		}
	}
	return n
}

func mod(a, n int64) int64 { return ((a % n) + n) % n }
