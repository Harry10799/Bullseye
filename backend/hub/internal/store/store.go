// Package store implements BoltDB-backed persistence for captured entries.
//
// BoltDB is a single-file embedded key-value store that's perfect for the
// hub's "small-to-medium cluster" use case (up to ~50 nodes, ~10k pps).
// For larger deployments, swap this implementation for a ClickHouse-backed
// one (see STORE.md for migration path).
//
// Schema:
//   bucket:entries      key=ULID(16 bytes)      val=msgpack(entry)
//   bucket:idx_time     key=timestamp(8 bytes)  val=ULID  (for time-range queries)
//   bucket:idx_svc      key=svc\x00ULID         val=empty (for service-filter queries)
//   bucket:meta         key="topology"          val=msgpack(topology)
package store

import (
        "context"
        "encoding/binary"
        "errors"
        "fmt"
        "math/rand"
        "time"

        "github.com/oklog/ulid/v2"
        "github.com/rs/zerolog/log"
        bolt "go.etcd.io/bbolt"
)

// Entry is the storage representation of a captured entry. The hub converts
// protobuf CaptureEntry → Entry before storing, so the storage layer is
// decoupled from the wire format.
type Entry struct {
        ID          string            `json:"id"`
        Timestamp   int64             `json:"timestamp"`
        Protocol    string            `json:"protocol"`
        TLS         bool              `json:"tls"`
        Source      Endpoint          `json:"source"`
        Destination Endpoint          `json:"destination"`
        Method      string            `json:"method,omitempty"`
        Path        string            `json:"path,omitempty"`
        Status      int32             `json:"status,omitempty"`
        LatencyMs   int32             `json:"latency_ms"`
        RequestID   string            `json:"request_id,omitempty"`
        IsRetry     bool              `json:"is_retry,omitempty"`
        Request     Payload           `json:"request"`
        Response    Payload           `json:"response"`
        GrpcStatus  string            `json:"grpc_status,omitempty"`
        DNSQuery    string            `json:"dns_query,omitempty"`
        DNSType     string            `json:"dns_type,omitempty"`
        DNSRcode    string            `json:"dns_rcode,omitempty"`
        DNSAnswer   []string          `json:"dns_answer,omitempty"`
        DNSTtl      int32             `json:"dns_ttl,omitempty"`
        NodeName    string            `json:"node_name,omitempty"`
        SnifferPod  string            `json:"sniffer_pod,omitempty"`
}

type Endpoint struct {
        IP       string `json:"ip"`
        Port     int32  `json:"port"`
        Pod      string `json:"pod,omitempty"`
        Service  string `json:"service,omitempty"`
        NS       string `json:"ns,omitempty"`
}

type Payload struct {
        Headers map[string]string `json:"headers,omitempty"`
        Body    []byte            `json:"body,omitempty"`
        Size    int32             `json:"size,omitempty"`
}

// Store is the persistence layer.
type Store struct {
        db *bolt.DB
}

var (
        bucketEntries = []byte("entries")
        bucketIdxTime = []byte("idx_time")
        bucketIdxSvc  = []byte("idx_svc")
        bucketMeta    = []byte("meta")

        // ErrNotFound is returned when an entry ID doesn't exist.
        ErrNotFound = errors.New("store: entry not found")
)

// New opens (or creates) a BoltDB file at path.
func New(path string) (*Store, error) {
        db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
        if err != nil {
                return nil, fmt.Errorf("store: open %s: %w", path, err)
        }

        // create buckets
        err = db.Update(func(tx *bolt.Tx) error {
                for _, b := range [][]byte{bucketEntries, bucketIdxTime, bucketIdxSvc, bucketMeta} {
                        if _, err := tx.CreateBucketIfNotExists(b); err != nil {
                                return fmt.Errorf("create bucket %s: %w", b, err)
                        }
                }
                return nil
        })
        if err != nil {
                db.Close()
                return nil, err
        }

        log.Info().Str("path", path).Msg("store: opened BoltDB")
        return &Store{db: db}, nil
}

// Close flushes and closes the database.
func (s *Store) Close() error {
        return s.db.Close()
}

// Put stores an entry and updates indexes atomically.
// If entry.ID is empty, a ULID is generated from the timestamp.
// If entry.ID is set (e.g., by a sniffer with its own ID scheme), it's used
// as-is and stored under a hash key for byte-sortability.
func (s *Store) Put(entry *Entry) error {
        if entry.ID == "" {
                now := time.UnixMilli(entry.Timestamp)
                entropy := ulid.Monotonic(rand.New(rand.NewSource(now.UnixNano())), 0)
                id := ulid.MustNew(ulid.Timestamp(now), entropy)
                entry.ID = id.String()
        }

        // serialize — using JSON for simplicity; switch to msgpack for ~30% size reduction
        val, err := encodeEntry(entry)
        if err != nil {
                return fmt.Errorf("store: encode: %w", err)
        }

        // primary key — use a composite key (timestamp + ID) so BoltDB iterates
        // in time order even for sniffer-supplied IDs that aren't ULIDs.
        // 8 bytes timestamp + raw ID bytes.
        primaryKey := make([]byte, 8)
        binary.BigEndian.PutUint64(primaryKey, uint64(entry.Timestamp))
        primaryKey = append(primaryKey, []byte(entry.ID)...)

        timeKey := make([]byte, 8)
        binary.BigEndian.PutUint64(timeKey, uint64(entry.Timestamp))

        return s.db.Update(func(tx *bolt.Tx) error {
                // primary
                if err := tx.Bucket(bucketEntries).Put(primaryKey, val); err != nil {
                        return err
                }
                // time index → primary key (so range queries can find entries)
                if err := tx.Bucket(bucketIdxTime).Put(timeKey, primaryKey); err != nil {
                        return err
                }
                // service index — both source and destination
                svcKey := func(svc, ns string) []byte {
                        return []byte(ns + "\x00" + svc + "\x00" + entry.ID)
                }
                if entry.Source.Service != "" {
                        _ = tx.Bucket(bucketIdxSvc).Put(svcKey(entry.Source.Service, entry.Source.NS), nil)
                }
                if entry.Destination.Service != "" {
                        _ = tx.Bucket(bucketIdxSvc).Put(svcKey(entry.Destination.Service, entry.Destination.NS), nil)
                }
                return nil
        })
}

// Get retrieves a single entry by ID. Scans entries bucket for a key
// ending with the given ID.
func (s *Store) Get(id string) (*Entry, error) {
        var val []byte
        err := s.db.View(func(tx *bolt.Tx) error {
                c := tx.Bucket(bucketEntries).Cursor()
                // prefix-scan for keys ending with []byte(id)
                for k, v := c.First(); k != nil; k, v = c.Next() {
                        if len(k) > 8 && string(k[8:]) == id {
                                val = v
                                return nil
                        }
                }
                return nil
        })
        if err != nil {
                return nil, err
        }
        if val == nil {
                return nil, ErrNotFound
        }
        return decodeEntry(val)
}

// Recent returns the most recent N entries (newest first).
func (s *Store) Recent(n int) ([]*Entry, error) {
        out := make([]*Entry, 0, n)
        err := s.db.View(func(tx *bolt.Tx) error {
                c := tx.Bucket(bucketEntries).Cursor()
                // BoltDB stores keys in byte-sorted order; ULID is time-ordered, so
                // descending iteration gives newest first.
                for k, v := c.Last(); k != nil && len(out) < n; k, v = c.Prev() {
                        e, err := decodeEntry(v)
                        if err != nil {
                                log.Warn().Err(err).Msg("store: decode entry failed")
                                continue
                        }
                        out = append(out, e)
                }
                return nil
        })
        return out, err
}

// Range returns entries in [from, to] timestamp range (inclusive, oldest first).
func (s *Store) Range(from, to int64) ([]*Entry, error) {
        out := make([]*Entry, 0, 256)
        fromKey := make([]byte, 8)
        toKey := make([]byte, 8)
        binary.BigEndian.PutUint64(fromKey, uint64(from))
        binary.BigEndian.PutUint64(toKey, uint64(to))

        err := s.db.View(func(tx *bolt.Tx) error {
                // entries bucket is keyed by (timestamp ++ id), so we can scan
                // the timestamp range directly.
                c := tx.Bucket(bucketEntries).Cursor()
                for k, v := c.Seek(fromKey); k != nil; k, v = c.Next() {
                        if len(k) < 8 {
                                continue
                        }
                        ts := int64(binary.BigEndian.Uint64(k[:8]))
                        if ts > to {
                                break
                        }
                        e, err := decodeEntry(v)
                        if err != nil {
                                continue
                        }
                        out = append(out, e)
                }
                return nil
        })
        return out, err
}

// Sweep deletes entries older than ttl. Returns the count deleted.
// Should be called on a ticker (e.g., every 5 minutes).
func (s *Store) Sweep(ttl time.Duration) (int, error) {
        cutoff := time.Now().Add(-ttl).UnixMilli()
        cutoffKey := make([]byte, 8)
        binary.BigEndian.PutUint64(cutoffKey, uint64(cutoff))

        deleted := 0
        err := s.db.Update(func(tx *bolt.Tx) error {
                c := tx.Bucket(bucketEntries).Cursor()
                var toDelete [][]byte
                for k, _ := c.First(); k != nil; k, _ = c.Next() {
                        if len(k) < 8 {
                                continue
                        }
                        if binary.BigEndian.Uint64(k[:8]) >= uint64(cutoff) {
                                break
                        }
                        toDelete = append(toDelete, append([]byte(nil), k...))
                }
                for _, k := range toDelete {
                        _ = tx.Bucket(bucketEntries).Delete(k)
                        deleted++
                }
                return nil
        })
        if err != nil {
                return 0, err
        }
        if deleted > 0 {
                log.Info().Int("deleted", deleted).Msg("store: sweep removed old entries")
        }
        return deleted, nil
}

// Count returns total entries stored.
func (s *Store) Count() int {
        var n int
        _ = s.db.View(func(tx *bolt.Tx) error {
                n = tx.Bucket(bucketEntries).Stats().KeyN
                return nil
        })
        return n
}

// PutMeta stores an arbitrary value under a meta key (e.g., cluster topology).
func (s *Store) PutMeta(key string, val []byte) error {
        return s.db.Update(func(tx *bolt.Tx) error {
                return tx.Bucket(bucketMeta).Put([]byte(key), val)
        })
}

// GetMeta retrieves a meta value.
func (s *Store) GetMeta(key string) ([]byte, error) {
        var val []byte
        err := s.db.View(func(tx *bolt.Tx) error {
                val = tx.Bucket(bucketMeta).Get([]byte(key))
                return nil
        })
        return val, err
}

// RunSweeper starts a background goroutine that periodically deletes old
// entries. Returns a stop function.
func (s *Store) RunSweeper(ctx context.Context, ttl, interval time.Duration) func() {
        ticker := time.NewTicker(interval)
        stop := make(chan struct{})
        go func() {
                defer ticker.Stop()
                for {
                        select {
                        case <-ctx.Done():
                                return
                        case <-stop:
                                return
                        case <-ticker.C:
                                if _, err := s.Sweep(ttl); err != nil {
                                        log.Warn().Err(err).Msg("store: sweep failed")
                                }
                        }
                }
        }()
        return func() { close(stop) }
}

// encodeEntry / decodeEntry — JSON for now, swap to msgpack later.
func encodeEntry(e *Entry) ([]byte, error) {
        return jsonMarshal(e)
}

func decodeEntry(b []byte) (*Entry, error) {
        var e Entry
        if err := jsonUnmarshal(b, &e); err != nil {
                return nil, err
        }
        return &e, nil
}
