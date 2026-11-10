// SPDX-License-Identifier: Apache-2.0
// Package tls contains the optional TLS-tracer client boundary for Bullseye.
//
// The tracer is a separate, optional process distributed outside this Apache-2.0
// repository. This package must contain only Apache-2.0 code and protocol-level
// integration; never copy or import GPL-licensed tracer implementation here.
//
// IMPORTANT: The gRPC transport and stream are not implemented yet. Start and
// LookupPlaintext currently provide a safe no-op boundary, not TLS decryption.
// Plaintext HTTP and DNS capture must continue to work when no tracer is present.
package tls

import (
	"context"
	"sync"

	"github.com/rs/zerolog/log"
)

// Decryptor is the Apache-2.0 client-side boundary for an optional tracer.
// tracerAddr is expected to be a loopback address when deployed as a Kubernetes
// sidecar in the same Pod network namespace.
type Decryptor struct {
	tracerAddr string

	mu      sync.RWMutex
	closed  bool
	plainBy string
}

// New creates an optional tracer client boundary. It does not require the
// tracer to be installed or running.
func New(tracerAddr string) *Decryptor {
	return &Decryptor{tracerAddr: tracerAddr}
}

// Start currently records the optional integration state and waits for
// cancellation. It deliberately does not fail collector startup if the tracer
// is absent. Replace this with a bounded, reconnecting gRPC stream when the
// separate tracer protocol is finalized.
func (d *Decryptor) Start(ctx context.Context) error {
	if d.tracerAddr == "" {
		log.Info().Msg("tls tracer disabled; continuing without TLS plaintext")
		<-ctx.Done()
		return nil
	}

	log.Info().Str("addr", d.tracerAddr).
		Msg("optional TLS tracer integration is not implemented; continuing without TLS plaintext")
	<-ctx.Done()
	return nil
}

// LookupPlaintext returns nil until the optional gRPC stream and bounded
// connection-tuple cache are implemented. Returning nil means the caller
// should retain opaque TLS metadata and continue normal packet processing.
func (d *Decryptor) LookupPlaintext(srcIP string, srcPort int, dstIP string, dstPort int) []byte {
	return nil
}
