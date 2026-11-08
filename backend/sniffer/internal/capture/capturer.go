// Package capture implements the packet capture layer.
//
// Two implementations:
//   - AF_PACKET (default, Apache-2.0): raw socket capture, no kernel privileges
//     beyond NET_RAW. Works on all kernels. Forked from Kubeshark's sniffer.
//   - eBPF (optional, GPL-3.0): uses cilium/ebpf to attach cgroup/socket
//     filters for higher performance + TLS decryption via uprobes.
//     See ebpf/README.md for the licensing boundary.
package capture

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
)

// Capturer is the interface both AF_PACKET and eBPF implementations satisfy.
type Capturer interface {
	// Start begins capturing packets on the configured interface(s).
	// Captured packets are sent to the dissector via the Packet channel.
	Start(ctx context.Context) error
	// Stop gracefully stops capture.
	Stop() error
	// Stats returns capture counters.
	Stats() Stats
}

// Stats holds capture-layer counters.
type Stats struct {
	PacketsReceived uint64
	PacketsDropped  uint64
	BytesReceived   uint64
}

// Config selects the capture backend.
type Config struct {
	Backend   string // "afpacket" (default) or "ebpf"
	Interface string // "any" or specific iface name
	Promisc   bool   // promiscuous mode
	BPFFilter string // tcpdump-style filter expression
}

// New creates a Capturer based on config.
func New(cfg Config) (Capturer, error) {
	switch cfg.Backend {
	case "", "afpacket":
		return NewAFPacket(cfg)
	case "ebpf":
		return NewEBPF(cfg)
	default:
		return nil, fmt.Errorf("unknown capture backend: %s", cfg.Backend)
	}
}

// NewAFPacket — placeholder until we vendor Kubeshark's AF_PACKET code.
// See ../../ebpf/README.md for extraction instructions.
func NewAFPacket(cfg Config) (Capturer, error) {
	log.Info().Str("iface", cfg.Interface).Bool("promisc", cfg.Promisc).
		Msg("afpacket: initializing (stub — vendor Kubeshark code)")
	return &stubCapturer{name: "afpacket"}, nil
}

// NewEBPF — placeholder. eBPF TLS tracer is GPL-3.0, lives in a separate
// sidecar container. See ../../ebpf/README.md.
func NewEBPF(cfg Config) (Capturer, error) {
	log.Info().Str("iface", cfg.Interface).
		Msg("ebpf: initializing (stub — see ebpf/README.md for TLS tracer setup)")
	return &stubCapturer{name: "ebpf"}, nil
}

// stubCapturer is a no-op capturer used until Kubeshark's AF_PACKET code
// is vendored. It logs a warning and does nothing.
type stubCapturer struct {
	name string
}

func (s *stubCapturer) Start(ctx context.Context) error {
	log.Warn().Str("backend", s.name).
		Msg("capture backend is a stub — vendor Kubeshark's sniffer code per backend/sniffer/VENDOR.md")
	<-ctx.Done()
	return nil
}

func (s *stubCapturer) Stop() error                  { return nil }
func (s *stubCapturer) Stats() Stats                  { return Stats{} }
