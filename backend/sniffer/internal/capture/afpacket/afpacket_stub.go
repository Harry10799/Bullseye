//go:build !libpcap
// +build !libpcap

// Package afpacket — stub implementation when built without libpcap.
//
// This stub is used in development environments where libpcap-dev isn't
// installed. It returns an error from New() so the sniffer falls back to
// fake-sniffer mode.
//
// For production, install libpcap-dev and rebuild with:
//   go build -tags libpcap ./cmd/sniffer
package afpacket

import (
	"context"
	"errors"

	"github.com/google/gopacket"
)

// ErrLibpcapNotCompiled is returned by New() when the binary was built
// without the `libpcap` build tag.
var ErrLibpcapNotCompiled = errors.New("afpacket: not compiled with libpcap support — rebuild with -tags libpcap and ensure libpcap-dev is installed")

// Capturer is a no-op stub.
type Capturer struct{}

// Stats is a stub.
type Stats struct {
	PacketsReceived uint64
	PacketsDropped  uint64
	BytesReceived   uint64
}

// Config matches the libpcap version's signature.
type Config struct {
	Interface string
	Promisc   bool
	Filter    string
	SnapLen   int
}

// New returns ErrLibpcapNotCompiled.
func New(cfg Config) (*Capturer, error) {
	return nil, ErrLibpcapNotCompiled
}

// Start is a no-op stub.
func (c *Capturer) Start(ctx context.Context) error { return nil }

// Stop is a no-op stub.
func (c *Capturer) Stop() error { return nil }

// Stats returns zeros.
func (c *Capturer) Stats() (uint64, uint64, uint64) { return 0, 0, 0 }

// Packets returns nil (no packets captured in stub mode).
func (c *Capturer) Packets() <-chan gopacket.Packet { return nil }

// Uptime returns 0.
func (c *Capturer) Uptime() interface{} { return 0 }
