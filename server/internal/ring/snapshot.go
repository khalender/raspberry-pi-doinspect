package ring

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The snapshot file: the store as gob, with the interval and ring length it was made with, so a
// change of either starts fresh instead of reading points into the wrong slots.
type snapshot struct {
	Version  int
	Interval time.Duration
	N        int64
	Series   map[string]snapSeries
	Extra    map[string]time.Time // the collector's last report per machine
}

type snapSeries struct {
	Head int64
	Vals []float32
}

const snapVersion = 1

// Save writes the store and extra to path through a temporary file and a rename, so a crash
// leaves the previous snapshot whole. It returns the file size.
func (s *Store) Save(path string, extra map[string]time.Time) (int64, error) {
	s.mu.RLock()
	snap := snapshot{Version: snapVersion, Interval: s.interval, N: s.n, Series: make(map[string]snapSeries, len(s.series)), Extra: extra}
	for k, r := range s.series {
		snap.Series[k] = snapSeries{Head: r.head, Vals: append([]float32(nil), r.vals...)}
	}
	s.mu.RUnlock()

	tmp, err := os.CreateTemp(filepath.Dir(path), ".snap-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriterSize(tmp, 1<<20)
	if err := gob.NewEncoder(w).Encode(snap); err != nil {
		tmp.Close()
		return 0, err
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return 0, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return 0, err
	}
	st, _ := tmp.Stat()
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// Load reads a snapshot into an empty store and returns its extra. A missing file is no error.
func (s *Store) Load(path string) (map[string]time.Time, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var snap snapshot
	if err := gob.NewDecoder(bufio.NewReaderSize(f, 1<<20)).Decode(&snap); err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", path, err)
	}
	if snap.Version != snapVersion || snap.Interval != s.interval || snap.N != s.n {
		return nil, fmt.Errorf("snapshot %s: made with interval %s and %d points, now %s and %d; starting empty", path, snap.Interval, snap.N, s.interval, s.n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range snap.Series {
		if int64(len(v.Vals)) == s.n {
			s.series[k] = &ring{head: v.Head, vals: v.Vals}
		}
	}
	return snap.Extra, nil
}
