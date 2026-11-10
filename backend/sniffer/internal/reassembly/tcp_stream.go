// SPDX-License-Identifier: Apache-2.0
// Package reassembly contains bounded TCP stream reconstruction primitives.
package reassembly

import (
	"bytes"
	"sync"
	"time"
)

// TCPConfig bounds per-flow and aggregate out-of-order buffering.
type TCPConfig struct {
	MaxFlows       int
	MaxBuffered    int
	MaxPerFlow     int
	MaxSegmentsPerFlow int
	IdleTimeout    time.Duration
}

// DefaultTCPConfig is deliberately conservative for an agent process.
func DefaultTCPConfig() TCPConfig {
	return TCPConfig{
		MaxFlows: 4096, MaxBuffered: 16 << 20, MaxPerFlow: 256 << 10,
		MaxSegmentsPerFlow: 128, IdleTimeout: 2 * time.Minute,
	}
}

type tcpSegment struct { seq uint32; data []byte }
type tcpFlow struct {
	next uint32
	initialized bool
	pending []tcpSegment
	buffered int
	lastSeen time.Time
}

// TCPAssembler reconstructs one direction of a TCP connection. It emits only
// contiguous bytes, ignores retransmitted bytes, buffers bounded out-of-order
// segments, and drops a flow when its limits are exceeded.
type TCPAssembler struct {
	mu sync.Mutex
	cfg TCPConfig
	flows map[StreamKey]*tcpFlow
	buffered int
	dropped uint64
}

// NewTCPAssembler creates an assembler. Non-positive limits use safe defaults.
func NewTCPAssembler(cfg TCPConfig) *TCPAssembler {
	d := DefaultTCPConfig()
	if cfg.MaxFlows <= 0 { cfg.MaxFlows = d.MaxFlows }
	if cfg.MaxBuffered <= 0 { cfg.MaxBuffered = d.MaxBuffered }
	if cfg.MaxPerFlow <= 0 { cfg.MaxPerFlow = d.MaxPerFlow }
	if cfg.MaxSegmentsPerFlow <= 0 { cfg.MaxSegmentsPerFlow = d.MaxSegmentsPerFlow }
	if cfg.IdleTimeout <= 0 { cfg.IdleTimeout = d.IdleTimeout }
	return &TCPAssembler{cfg: cfg, flows: make(map[StreamKey]*tcpFlow)}
}

// Push adds one TCP segment. seq is the TCP sequence number of payload[0];
// syn must be true for a SYN packet (whose sequence space consumes one byte).
// The returned slice contains newly contiguous payload and is owned by caller.
func (a *TCPAssembler) Push(key StreamKey, seq uint32, syn bool, payload []byte) []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	a.expireLocked(now)
	f := a.flows[key]
	if f == nil {
		if len(a.flows) >= a.cfg.MaxFlows { a.dropped++; return nil }
		f = &tcpFlow{lastSeen: now}
		a.flows[key] = f
	}
	f.lastSeen = now
	if syn {
		seq++
		if !f.initialized { f.next, f.initialized = seq, true }
	}
	if len(payload) == 0 { return nil }
	if !f.initialized { f.next, f.initialized = seq, true }

	// TCP sequence comparisons use signed 32-bit deltas to handle wraparound.
	delta := int32(seq - f.next)
	if delta < 0 {
		trim := int64(-int64(delta))
		if trim >= int64(len(payload)) { return nil }
		payload = payload[trim:]
		seq = f.next
		delta = 0
	}
	if delta == 0 {
		// A newly contiguous segment that overlaps buffered future bytes makes
		// first-seen/last-seen byte selection ambiguous. Drop rather than emit
		// a potentially corrupted stream.
		end := seq + uint32(len(payload))
		for _, old := range f.pending {
			oldEnd := old.seq + uint32(len(old.data))
			if int32(old.seq-end) < 0 && int32(seq-oldEnd) < 0 {
				a.dropFlowLocked(key, f)
				a.dropped++
				return nil
			}
		}
		out := append([]byte(nil), payload...)
		f.next += uint32(len(payload))
		out = append(out, a.drainLocked(f)...)
		return out
	}

	// Out-of-order data is bounded before it is copied. Segment-count
	// limits also bound slice metadata when an attacker sends tiny gaps.
	if len(f.pending) >= a.cfg.MaxSegmentsPerFlow {
		a.dropFlowLocked(key, f)
		a.dropped++
		return nil
	}
	if len(payload) > a.cfg.MaxPerFlow-f.buffered || len(payload) > a.cfg.MaxBuffered-a.buffered {
		a.dropFlowLocked(key, f)
		a.dropped++
		return nil
	}
	// Exact duplicate segments are ignored. Overlapping segments are rejected
	// rather than choosing ambiguous bytes from conflicting retransmissions.
	for _, old := range f.pending {
		oldEnd := old.seq + uint32(len(old.data))
		newEnd := seq + uint32(len(payload))
		if seq == old.seq && bytes.Equal(payload, old.data) { return nil }
		if int32(seq-oldEnd) < 0 && int32(old.seq-newEnd) < 0 {
			a.dropFlowLocked(key, f)
			a.dropped++
			return nil
		}
	}
	f.pending = append(f.pending, tcpSegment{seq: seq, data: append([]byte(nil), payload...)})
	f.buffered += len(payload)
	a.buffered += len(payload)
	return nil
}

func (a *TCPAssembler) drainLocked(f *tcpFlow) []byte {
	var out []byte
	for {
		found := -1
		for i := range f.pending {
			if f.pending[i].seq == f.next { found = i; break }
		}
		if found < 0 { break }
		seg := f.pending[found]
		f.pending = append(f.pending[:found], f.pending[found+1:]...)
		f.buffered -= len(seg.data)
		a.buffered -= len(seg.data)
		out = append(out, seg.data...)
		f.next += uint32(len(seg.data))
	}
	return out
}

func (a *TCPAssembler) dropFlowLocked(key StreamKey, f *tcpFlow) {
	a.buffered -= f.buffered
	delete(a.flows, key)
}

// Close discards one directional flow and releases buffered memory.
func (a *TCPAssembler) Close(key StreamKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if f := a.flows[key]; f != nil { a.dropFlowLocked(key, f) }
}

// GC expires idle flows and releases all buffered memory.
func (a *TCPAssembler) GC() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireLocked(time.Now())
}

func (a *TCPAssembler) expireLocked(now time.Time) {
	for key, f := range a.flows {
		if now.Sub(f.lastSeen) > a.cfg.IdleTimeout { a.dropFlowLocked(key, f) }
	}
}

// Stats returns active flow count, buffered bytes and dropped-flow count.
func (a *TCPAssembler) Stats() (flows, buffered int, dropped uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.flows), a.buffered, a.dropped
}

