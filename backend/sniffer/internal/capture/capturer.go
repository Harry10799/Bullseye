// Package capture implements the packet capture layer.
//
// Two implementations:
//   - AF_PACKET (default, Apache-2.0): real libpcap capture via gopacket.
//     Requires CAP_NET_RAW + CAP_NET_ADMIN. This is the production backend.
//   - eBPF (optional, GPL-3.0): uses cilium/ebpf to attach cgroup/socket
//     filters for higher performance + TLS decryption via uprobes.
//     See ebpf/README.md for the licensing boundary.
package capture

import (
        "context"
        "fmt"

        "github.com/bullseye/bullseye/backend/sniffer/internal/capture/afpacket"
        "github.com/google/gopacket"
        "github.com/rs/zerolog/log"
)

// Capturer is the interface both AF_PACKET and eBPF implementations satisfy.
type Capturer interface {
        // Start begins capturing packets on the configured interface(s).
        Start(ctx context.Context) error
        // Stop gracefully stops capture.
        Stop() error
        // Stats returns capture counters.
        Stats() Stats
        // Packets returns captured, decoded packets.
        Packets() <-chan gopacket.Packet
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
        SnapLen   int    // max packet capture size
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

// NewAFPacket creates a real libpcap-backed capturer (when built with
// -tags libpcap and libpcap-dev installed) or returns an error in stub mode.
func NewAFPacket(cfg Config) (Capturer, error) {
        c, err := afpacket.New(afpacket.Config{
                Interface: cfg.Interface,
                Promisc:   cfg.Promisc,
                Filter:    cfg.BPFFilter,
                SnapLen:   cfg.SnapLen,
        })
        if err != nil {
                return nil, err
        }
        return &afpacketAdapter{c: c}, nil
}

// afpacketAdapter wraps the afpacket.Capturer to satisfy the Capturer interface.
type afpacketAdapter struct {
        c *afpacket.Capturer
}

func (a *afpacketAdapter) Start(ctx context.Context) error {
        return a.c.Start(ctx)
}
func (a *afpacketAdapter) Stop() error {
        return a.c.Stop()
}
func (a *afpacketAdapter) Stats() Stats {
        r, d, b := a.c.Stats()
        return Stats{PacketsReceived: r, PacketsDropped: d, BytesReceived: b}
}
func (a *afpacketAdapter) Packets() <-chan gopacket.Packet {
        return a.c.Packets()
}

// NewEBPF — placeholder. eBPF TLS tracer is GPL-3.0, lives in a separate
// sidecar container. See ../../ebpf/README.md.
func NewEBPF(cfg Config) (Capturer, error) {
        log.Info().Str("iface", cfg.Interface).
                Msg("ebpf: initializing (stub — see ebpf/README.md for TLS tracer setup)")
        return &stubCapturer{name: "ebpf"}, nil
}

// stubCapturer is a no-op capturer used for the eBPF backend until the
// GPL-3.0 tracer sidecar is wired up.
type stubCapturer struct {
        name string
}

func (s *stubCapturer) Start(ctx context.Context) error {
        log.Warn().Str("backend", s.name).
                Msg("capture backend is a stub — vendor Kubeshark's sniffer code per backend/sniffer/VENDOR.md")
        <-ctx.Done()
        return nil
}

func (s *stubCapturer) Stop() error  { return nil }
func (s *stubCapturer) Stats() Stats { return Stats{} }
func (s *stubCapturer) Packets() <-chan gopacket.Packet {
	return nil
}
