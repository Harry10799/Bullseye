// Package reassembly tracks TCP streams for HTTP request/response pairing.
//
// This is a simplified v1 implementation that handles HTTP/1.x keep-alive
// connections. It tracks per-connection request queues so responses can be
// matched to their corresponding requests in pipelined or keep-alive
// connections.
//
// v2 will use gopacket's full TCP reassembler for out-of-order packets
// and HTTP/2 support.
package reassembly

import (
	"sync"
	"time"

	capturev1 "github.com/bullseye/bullseye/backend/proto/bullseye/v1"
	httpdissect "github.com/bullseye/bullseye/backend/sniffer/internal/dissect/http"
	"github.com/rs/zerolog/log"
)

// StreamKey uniquely identifies one direction of a TCP stream.
type StreamKey struct {
	SrcIP   string
	SrcPort int
	DstIP   string
	DstPort int
}

// String returns a stable string representation.
func (s StreamKey) String() string {
	return s.SrcIP + ":" + itoa(s.SrcPort) + "→" + s.DstIP + ":" + itoa(s.DstPort)
}

// connection holds state for one TCP connection (both directions).
type connection struct {
	clientDissect *httpdissect.Dissector // requests (client → server)
	serverDissect *httpdissect.Dissector // responses (server → client)
	pairer        *httpdissect.Pairer
	lastSeen      time.Time
}

// Tracker manages per-connection state and emits complete HTTP pairs.
type Tracker struct {
	mu         sync.Mutex
	conns      map[string]*connection
	out        chan<- *capturev1.CaptureEntry
	snifferPod string
	nodeName   string
	ttl        time.Duration

	stats Stats
}

// Stats holds tracker counters.
type Stats struct {
	StreamsActive  int
	StreamsClosed  int
	EntriesEmitted int
}

// New creates a Tracker.
func New(out chan<- *capturev1.CaptureEntry, snifferPod, nodeName string) *Tracker {
	return &Tracker{
		conns:      make(map[string]*connection),
		out:        out,
		snifferPod: snifferPod,
		nodeName:   nodeName,
		ttl:        5 * time.Minute,
	}
}

// FeedHTTP pushes HTTP message bytes from one direction of a connection.
// direction=true means client→server (requests), false means server→client (responses).
func (t *Tracker) FeedHTTP(key StreamKey, data []byte, direction bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	connKey := connKey(key)
	conn, ok := t.conns[connKey]
	if !ok {
		conn = &connection{
			clientDissect: httpdissect.New(),
			serverDissect: httpdissect.New(),
			pairer:        httpdissect.NewPairer(),
		}
		t.conns[connKey] = conn
		t.stats.StreamsActive++
		log.Debug().Str("conn", connKey).Msg("reassembly: new connection")
	}
	conn.lastSeen = time.Now()

	var dissector *httpdissect.Dissector
	if direction {
		dissector = conn.clientDissect
	} else {
		dissector = conn.serverDissect
	}

	msgs, err := dissector.Feed(data)
	if err != nil {
		log.Debug().Err(err).Str("conn", connKey).Msg("reassembly: feed error")
	}

	for _, msg := range msgs {
		if msg.IsRequest {
			conn.pairer.PushRequest(msg)
		} else {
			pair := conn.pairer.MatchResponse(msg)
			if pair != nil {
				entry := t.buildEntry(key, pair)
				select {
				case t.out <- entry:
					t.stats.EntriesEmitted++
				default:
					// output full — drop
				}
			}
		}
	}
}

// buildEntry converts an HTTP request/response pair to a CaptureEntry.
func (t *Tracker) buildEntry(key StreamKey, pair *httpdissect.Pair) *capturev1.CaptureEntry {
	now := time.Now().UnixMilli()
	req := pair.Request
	resp := pair.Response

	entry := &capturev1.CaptureEntry{
		Id:         "pkt_" + itoa64(now) + "_" + randHex(8),
		Timestamp:  now,
		Protocol:   capturev1.Protocol_HTTP,
		Tls:        false,
		Method:     req.Method,
		Path:       req.Path,
		NodeName:   t.nodeName,
		SnifferPod: t.snifferPod,
		Source: &capturev1.Endpoint{
			Ip:   key.SrcIP,
			Port: int32(key.SrcPort),
		},
		Destination: &capturev1.Endpoint{
			Ip:   key.DstIP,
			Port: int32(key.DstPort),
		},
		Request: &capturev1.Payload{
			Headers: req.Headers,
			Body:    req.Body,
			Size:    int32(len(req.Body)),
		},
	}
	if resp != nil {
		entry.Status = int32(resp.Status)
		entry.Response = &capturev1.Payload{
			Headers: resp.Headers,
			Body:    resp.Body,
			Size:    int32(len(resp.Body)),
		}
	}
	return entry
}

// GC removes connections idle for longer than ttl. Call on a ticker.
func (t *Tracker) GC() {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := time.Now().Add(-t.ttl)
	for key, c := range t.conns {
		if c.lastSeen.Before(cutoff) {
			delete(t.conns, key)
			t.stats.StreamsActive--
			t.stats.StreamsClosed++
		}
	}
}

// RunGC starts a goroutine that periodically calls GC.
func (t *Tracker) RunGC(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			t.GC()
		}
	}
}

// Stats returns a snapshot.
func (t *Tracker) Stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

// connKey returns a stable key for a connection (independent of direction).
func connKey(s StreamKey) string {
	if s.SrcIP < s.DstIP || (s.SrcIP == s.DstIP && s.SrcPort < s.DstPort) {
		return s.SrcIP + ":" + itoa(s.SrcPort) + "-" + s.DstIP + ":" + itoa(s.DstPort)
	}
	return s.DstIP + ":" + itoa(s.DstPort) + "-" + s.SrcIP + ":" + itoa(s.SrcPort)
}

// itoa is a small helper to avoid strconv import in this hot path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// itoa64 is itoa for int64.
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// randHex generates a random hex string of length n.
// Uses time-based pseudo-random — sufficient for entry IDs.
func randHex(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	now := time.Now().UnixNano()
	for i := range b {
		b[i] = hex[(now+int64(i))%16]
	}
	return string(b)
}
