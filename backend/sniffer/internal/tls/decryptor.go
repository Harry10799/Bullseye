// Package tls — TLS decryption via eBPF uprobes.
//
// LICENSE BOUNDARY:
//   This package is a thin glue layer. The actual eBPF C programs that hook
//   SSL_write/SSL_read (OpenSSL), crypto/tls (Go), and Java's SSLEngine
//   are GPL-3.0 code forked from kubeshark/tracer.
//
//   To keep Bullseye's core Apache-2.0, the TLS tracer runs as a SEPARATE
//   sidecar container (see ../../deploy/tls-tracer-sidecar.yaml) and
//   communicates with the sniffer via a localhost gRPC port. The Apache
//   sniffer binary contains NO GPL code.
//
//   If you want a single-binary distribution, you must either:
//     (a) relicense Bullseye to GPL-3.0, OR
//     (b) write your own eBPF TLS tracer from scratch (cilium/ebpf + libbpf).
package tls

import (
	"context"
	"github.com/rs/zerolog/log"
)

// Decryptor connects to the TLS tracer sidecar and receives decrypted
// plaintext for TLS-encrypted connections.
type Decryptor struct {
	tracerAddr string // localhost:9091 (sidecar)
}

// New creates a Decryptor connected to the tracer sidecar.
func New(tracerAddr string) *Decryptor {
	return &Decryptor{tracerAddr: tracerAddr}
}

// Start connects to the sidecar and begins receiving plaintext.
// Until this is called, TLS traffic is opaque to the sniffer.
func (d *Decryptor) Start(ctx context.Context) error {
	log.Info().Str("addr", d.tracerAddr).
		Msg("tls: connecting to tracer sidecar (GPL-3.0 component)")
	// TODO: gRPC stream to sidecar
	// The sidecar sends decrypted plaintext tagged with the originating
	// (srcIP, srcPort, dstIP, dstPort) tuple. The sniffer matches these
	// against captured encrypted packets and replaces the ciphertext
	// with plaintext before dissection.
	<-ctx.Done()
	return nil
}

// LookupPlaintext returns the decrypted bytes for a given connection tuple,
// or nil if not available. Called by the dissector when it detects TLS.
func (d *Decryptor) LookupPlaintext(srcIP string, srcPort int, dstIP string, dstPort int) []byte {
	// TODO: lookup in the sidecar's plaintext cache
	return nil
}
