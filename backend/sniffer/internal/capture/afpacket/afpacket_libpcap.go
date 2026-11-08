//go:build libpcap
// +build libpcap

// Package afpacket implements real packet capture using libpcap via gopacket.
//
// This is the production capture backend. It opens a libpcap handle on the
// specified interface (or "any" for all interfaces), applies a BPF filter,
// and emits raw packets to the dissector pipeline.
//
// Capabilities required (set in DaemonSet):
//   - CAP_NET_RAW  (for AF_PACKET raw socket)
//   - CAP_NET_ADMIN (for promiscuous mode)
//
// In production, run as hostNetwork: true so the sniffer sees all pod traffic.
// See backend/deploy/20-sniffer-daemonset.yaml.
//
// Build with: go build -tags libpcap ./cmd/sniffer
// (requires libpcap-dev installed)
package afpacket

import (
        "context"
        "fmt"
        "sync/atomic"
        "time"

        "github.com/google/gopacket"
        "github.com/google/gopacket/pcap"
        "github.com/rs/zerolog/log"
)

// Capturer wraps a libpcap handle and emits gopacket.Packet values.
type Capturer struct {
        iface   string
        promisc bool
        filter  string
        snaplen int

        handle    *pcap.Handle
        packets   chan gopacket.Packet
        stop      chan struct{}
        stats     Stats
        startTime time.Time
}

// Stats holds capture-layer counters (atomic for thread-safe reads).
type Stats struct {
        PacketsReceived atomic.Uint64
        PacketsDropped  atomic.Uint64
        BytesReceived   atomic.Uint64
}

// Config selects capture parameters.
type Config struct {
        Interface string // "any", "eth0", etc.
        Promisc   bool
        Filter    string // BPF filter expression
        SnapLen   int    // max packet capture size (default 65535)
}

// New creates a Capturer. Call Start() to begin capturing.
func New(cfg Config) (*Capturer, error) {
        if cfg.SnapLen == 0 {
                cfg.SnapLen = 65535
        }
        if cfg.Interface == "" {
                cfg.Interface = "any"
        }
        c := &Capturer{
                iface:   cfg.Interface,
                promisc: cfg.Promisc,
                filter:  cfg.Filter,
                snaplen: cfg.SnapLen,
                packets: make(chan gopacket.Packet, 4096),
                stop:    make(chan struct{}),
        }

        log.Info().
                Str("iface", c.iface).
                Bool("promisc", c.promisc).
                Int("snaplen", c.snaplen).
                Str("filter", c.filter).
                Msg("afpacket: opening capture handle")

        inactive, err := pcap.NewInactiveHandle(c.iface)
        if err != nil {
                return nil, fmt.Errorf("afpacket: new inactive handle: %w", err)
        }
        defer inactive.CleanUp()

        if err := inactive.SetPromisc(c.promisc); err != nil {
                return nil, fmt.Errorf("afpacket: set promisc: %w", err)
        }
        if err := inactive.SetSnapLen(c.snaplen); err != nil {
                return nil, fmt.Errorf("afpacket: set snaplen: %w", err)
        }
        if err := inactive.SetTimeout(time.Second); err != nil {
                return nil, fmt.Errorf("afpacket: set timeout: %w", err)
        }
        if err := inactive.SetBufferSize(16 * 1024 * 1024); err != nil {
                log.Warn().Err(err).Msg("afpacket: set buffer size failed (continuing)")
        }

        handle, err := inactive.Activate()
        if err != nil {
                return nil, fmt.Errorf("afpacket: activate (need CAP_NET_RAW?): %w", err)
        }
        c.handle = handle

        if c.filter != "" {
                if err := handle.SetBPFFilter(c.filter); err != nil {
                        handle.Close()
                        return nil, fmt.Errorf("afpacket: set BPF filter %q: %w", c.filter, err)
                }
        }

        log.Info().Str("iface", c.iface).Msg("afpacket: capture handle active")
        return c, nil
}

// Start begins reading packets in a goroutine.
func (c *Capturer) Start(ctx context.Context) error {
        c.startTime = time.Now()

        go func() {
                defer close(c.packets)
                for {
                        select {
                        case <-ctx.Done():
                                log.Info().Msg("afpacket: stopping (context canceled)")
                                return
                        case <-c.stop:
                                log.Info().Msg("afpacket: stopping (stop signal)")
                                return
                        default:
                                pkt, err := c.handle.ReadPacketData()
                                if err != nil {
                                        if err == pcap.NextErrorTimeoutExpired {
                                                continue
                                        }
                                        log.Error().Err(err).Msg("afpacket: read packet failed")
                                        return
                                }
                                c.stats.PacketsReceived.Add(1)
                                c.stats.BytesReceived.Add(uint64(len(pkt)))

                                packet := gopacket.NewPacket(pkt, c.handle.LinkType(), gopacket.Default)
                                select {
                                case c.packets <- packet:
                                default:
                                        c.stats.PacketsDropped.Add(1)
                                }
                        }
                }
        }()

        return nil
}

// Packets returns the channel of captured packets.
func (c *Capturer) Packets() <-chan gopacket.Packet {
        return c.packets
}

// Stop gracefully stops capture.
func (c *Capturer) Stop() error {
        select {
        case <-c.stop:
        default:
                close(c.stop)
        }
        if c.handle != nil {
                c.handle.Close()
        }
        return nil
}

// Stats returns capture counters.
func (c *Capturer) Stats() (received, dropped, bytes uint64) {
        return c.stats.PacketsReceived.Load(),
                c.stats.PacketsDropped.Load(),
                c.stats.BytesReceived.Load()
}

// Uptime returns how long capture has been running.
func (c *Capturer) Uptime() time.Duration {
        if c.startTime.IsZero() {
                return 0
        }
        return time.Since(c.startTime)
}
