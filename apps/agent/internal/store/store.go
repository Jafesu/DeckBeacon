// Package store is the agent's durable state (spec 03): the bounded outbound
// event queue with persisted sequence numbers, and the command dedupe table.
// Writes are transactional (bbolt) so a crash never replays or loses an ack.
package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bEvents   = []byte("events")
	bMeta     = []byte("meta")
	bCommands = []byte("commands")
	kNextSeq  = []byte("next_event_seq")
	kAcked    = []byte("acked_event_seq")
	kDropped  = []byte("dropped_events")
)

// Event is a queued device event (protocol event body).
type Event struct {
	EventID    string          `json:"event_id"`
	EventSeq   uint64          `json:"event_seq"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Live       bool            `json:"live"`
	Data       json.RawMessage `json:"data"`
}

// CommandRecord is the last known state of a command, kept for dedupe.
type CommandRecord struct {
	State string    `json:"state"`
	At    time.Time `json:"at"`
}

// Store wraps a bbolt database.
type Store struct {
	db        *bolt.DB
	maxEvents int
}

// Open opens (or creates) the store. maxEvents bounds the unacked queue.
func Open(path string, maxEvents int) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bEvents, bMeta, bCommands} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, maxEvents: maxEvents}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func u64(b []byte) uint64 {
	if len(b) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func be(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return b[:]
}

// AppendEvent assigns the next persisted sequence number and queues the event.
// When the queue is full the oldest unacked event is dropped and counted.
func (s *Store) AppendEvent(e Event) (Event, error) {
	err := s.db.Update(func(tx *bolt.Tx) error {
		meta, evs := tx.Bucket(bMeta), tx.Bucket(bEvents)
		next := u64(meta.Get(kNextSeq))
		if next == 0 {
			next = 1
		}
		e.EventSeq = next
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := evs.Put(be(next), raw); err != nil {
			return err
		}
		if err := meta.Put(kNextSeq, be(next+1)); err != nil {
			return err
		}
		for countKeys(evs) > s.maxEvents {
			k, _ := evs.Cursor().First()
			if k == nil {
				break
			}
			if err := evs.Delete(k); err != nil {
				return err
			}
			if err := meta.Put(kDropped, be(u64(meta.Get(kDropped))+1)); err != nil {
				return err
			}
		}
		return nil
	})
	return e, err
}

// countKeys counts keys with a cursor (Bucket.Stats is not reliable inside a
// write transaction). The queue is bounded, so this stays cheap.
func countKeys(b *bolt.Bucket) int {
	n := 0
	c := b.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		n++
	}
	return n
}

// PendingEvents returns queued events with seq > after, oldest first, up to limit.
func (s *Store) PendingEvents(after uint64, limit int) ([]Event, error) {
	var out []Event
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bEvents).Cursor()
		for k, v := c.Seek(be(after + 1)); k != nil && len(out) < limit; k, v = c.Next() {
			var e Event
			if err := json.Unmarshal(v, &e); err != nil {
				return err
			}
			out = append(out, e)
		}
		return nil
	})
	return out, err
}

// AckThrough removes all events with seq <= seq and records the ack watermark.
func (s *Store) AckThrough(seq uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		meta, evs := tx.Bucket(bMeta), tx.Bucket(bEvents)
		if seq > u64(meta.Get(kAcked)) {
			if err := meta.Put(kAcked, be(seq)); err != nil {
				return err
			}
		}
		c := evs.Cursor()
		for k, _ := c.First(); k != nil && u64(k) <= seq; k, _ = c.First() {
			if err := evs.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// Stats reports queue state.
type Stats struct {
	Pending int
	Acked   uint64
	NextSeq uint64
	Dropped uint64
}

// Stats returns queue statistics.
func (s *Store) Stats() (Stats, error) {
	var st Stats
	err := s.db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket(bMeta)
		st.Pending = countKeys(tx.Bucket(bEvents))
		st.Acked, st.NextSeq, st.Dropped = u64(meta.Get(kAcked)), u64(meta.Get(kNextSeq)), u64(meta.Get(kDropped))
		return nil
	})
	return st, err
}

// Meta returns a stored metadata string ("" if unset).
func (s *Store) Meta(key string) (string, error) {
	var v string
	err := s.db.View(func(tx *bolt.Tx) error {
		v = string(tx.Bucket(bMeta).Get([]byte("m:" + key)))
		return nil
	})
	return v, err
}

// SetMeta stores a metadata string.
func (s *Store) SetMeta(key, value string) error {
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(bMeta).Put([]byte("m:"+key), []byte(value)) })
}

// ErrDuplicate is returned by BeginCommand for a command already seen.
var ErrDuplicate = errors.New("duplicate command")

// BeginCommand records a new command id atomically; a second call with the same
// id returns ErrDuplicate and the earlier record (durable across restarts).
func (s *Store) BeginCommand(id string, now time.Time) (CommandRecord, error) {
	var prev CommandRecord
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bCommands)
		if v := b.Get([]byte(id)); v != nil {
			if err := json.Unmarshal(v, &prev); err != nil {
				return err
			}
			return ErrDuplicate
		}
		raw, _ := json.Marshal(CommandRecord{State: "accepted", At: now})
		return b.Put([]byte(id), raw)
	})
	return prev, err
}

// FinishCommand stores the terminal state of a command.
func (s *Store) FinishCommand(id, state string, now time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		raw, _ := json.Marshal(CommandRecord{State: state, At: now})
		return tx.Bucket(bCommands).Put([]byte(id), raw)
	})
}

// PruneCommands deletes dedupe records older than cutoff. Commands have TTL <= 24h,
// so records older than that can never be legitimately replayed.
func (s *Store) PruneCommands(cutoff time.Time) (int, error) {
	n := 0
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bCommands)
		var stale [][]byte
		err := b.ForEach(func(k, v []byte) error {
			var r CommandRecord
			if json.Unmarshal(v, &r) == nil && r.At.Before(cutoff) {
				stale = append(stale, append([]byte(nil), k...))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range stale {
			if err := b.Delete(k); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}
