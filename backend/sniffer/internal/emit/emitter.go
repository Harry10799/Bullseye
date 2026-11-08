// Package emit sends enriched CaptureEntry messages to the hub via gRPC.
package emit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Emitter sends entries to the hub via a long-lived gRPC stream.
type Emitter struct {
	hubAddr    string
	conn       *grpc.ClientConn
	stream     interface{} // TODO: replace with capturev1.CaptureService_StreamEntriesClient
	mu         sync.Mutex
	pending    int
	maxPending int
}

// New creates an Emitter connected to hubAddr (e.g., "bullseye-hub:9090").
func New(ctx context.Context, hubAddr string) (*Emitter, error) {
	conn, err := grpc.NewClient(hubAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16*1024*1024)),
	)
	if err != nil {
		return nil, fmt.Errorf("emit: dial hub: %w", err)
	}
	log.Info().Str("addr", hubAddr).Msg("emit: connected to hub")

	e := &Emitter{
		hubAddr:    hubAddr,
		conn:       conn,
		maxPending: 1024,
	}

	// open stream
	// TODO after gen-proto.sh:
	// client := capturev1.NewCaptureServiceClient(conn)
	// e.stream, err = client.StreamEntries(ctx)
	// if err != nil { return nil, err }

	return e, nil
}

// Send queues an entry for transmission to the hub. Non-blocking.
// If the pending queue is full, the entry is dropped (sampling).
func (e *Emitter) Send(entry interface{}) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending >= e.maxPending {
		return fmt.Errorf("emit: pending queue full (%d), dropping", e.pending)
	}
	e.pending++
	// TODO: e.stream.Send(entry.(*capturev1.CaptureEntry))
	return nil
}

// Close gracefully closes the stream and connection.
func (e *Emitter) Close() error {
	// TODO: e.stream.CloseSend()
	return e.conn.Close()
}

// RunReconnect keeps the stream alive, reconnecting on failure.
// Call this in a goroutine.
func (e *Emitter) RunReconnect(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// health check — if stream is dead, reconnect
			e.mu.Lock()
			dead := e.stream == nil
			e.mu.Unlock()
			if dead {
				log.Warn().Msg("emit: stream dead, reconnecting")
				// TODO: reopen stream
			}
		}
	}
}
