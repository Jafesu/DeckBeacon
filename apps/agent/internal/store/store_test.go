package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T, path string, max int) *Store {
	t.Helper()
	s, err := Open(path, max)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEventSequencePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	s := open(t, path, 100)
	for i := 0; i < 3; i++ {
		if _, err := s.AppendEvent(Event{EventType: "device_resumed"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AckThrough(2); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = open(t, path, 100)
	defer s.Close()
	e, err := s.AppendEvent(Event{EventType: "device_resumed"})
	if err != nil {
		t.Fatal(err)
	}
	if e.EventSeq != 4 {
		t.Fatalf("seq after restart = %d, want 4", e.EventSeq)
	}
	pend, _ := s.PendingEvents(0, 10)
	if len(pend) != 2 || pend[0].EventSeq != 3 || pend[1].EventSeq != 4 {
		t.Fatalf("pending = %+v", pend)
	}
	pend, _ = s.PendingEvents(3, 10)
	if len(pend) != 1 || pend[0].EventSeq != 4 {
		t.Fatalf("pending after 3 = %+v", pend)
	}
	st, _ := s.Stats()
	if st.Acked != 2 || st.NextSeq != 5 {
		t.Fatalf("stats %+v", st)
	}
}

func TestQueueIsBounded(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "s.db"), 3)
	defer s.Close()
	for i := 0; i < 5; i++ {
		_, _ = s.AppendEvent(Event{EventType: "storage_low"})
	}
	st, _ := s.Stats()
	if st.Pending != 3 || st.Dropped != 2 {
		t.Fatalf("stats %+v", st)
	}
	pend, _ := s.PendingEvents(0, 10)
	if pend[0].EventSeq != 3 {
		t.Fatalf("oldest kept = %d, want 3", pend[0].EventSeq)
	}
}

func TestCommandDedupeDurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	s := open(t, path, 10)
	now := time.Unix(1000, 0)
	if _, err := s.BeginCommand("C1", now); err != nil {
		t.Fatal(err)
	}
	_ = s.FinishCommand("C1", "rejected", now)
	s.Close()
	s = open(t, path, 10)
	defer s.Close()
	prev, err := s.BeginCommand("C1", now)
	if !errors.Is(err, ErrDuplicate) || prev.State != "rejected" {
		t.Fatalf("got %v %+v", err, prev)
	}
	n, _ := s.PruneCommands(now.Add(time.Hour))
	if n != 1 {
		t.Fatalf("pruned %d", n)
	}
	if _, err := s.BeginCommand("C1", now); err != nil {
		t.Fatal("pruned id should be accepted again")
	}
}
