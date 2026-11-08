// Package dissect reassembles raw packets into L7 protocol messages.
//
// Dissectors are forked from Kubeshark's misc/ package (Apache-2.0). Each
// dissector takes a reassembled TCP stream and emits zero or more L7
// messages (request/response pairs).
//
// Supported protocols (after vendoring Kubeshark's code):
//   HTTP/1.0, HTTP/1.1, HTTP/2 (gRPC), DNS, Kafka, Redis, AMQP, MySQL, MongoDB
package dissect

import (
	"context"
)

// Dissector is the interface each protocol dissector satisfies.
type Dissector interface {
	// Name returns the protocol name (HTTP, gRPC, DNS, etc.)
	Name() string
	// Port returns the well-known port(s) this dissector listens on.
	Ports() []int
	// Dissect takes a reassembled TCP stream (one direction) and emits
	// Messages. For request/response protocols, both directions are
	// dissected separately and paired by the StreamTracker.
	Dissect(ctx context.Context, stream *Stream, out chan<- *Message) error
}

// Stream is a reassembled TCP stream (one direction).
type Stream struct {
	SrcIP   string
	SrcPort int
	DstIP   string
	DstPort int
	Data    []byte // full reassembled payload
}

// Message is a dissected L7 message (request or response).
type Message struct {
	Protocol string
	IsRequest bool
	Method   string
	Path     string
	Status   int
	Headers  map[string]string
	Body     []byte
	// DNS-specific
	DNSQuery string
	DNSType  string
	DNSRcode string
	DNSAnswer []string
	DNSTtl   int
	// gRPC-specific
	GrpcMethod string
	GrpcStatus string
}

// Registry holds all registered dissectors keyed by port.
type Registry struct {
	byPort map[int]Dissector
	all    []Dissector
}

// NewRegistry creates and populates the dissector registry.
func NewRegistry() *Registry {
	r := &Registry{byPort: make(map[int]Dissector)}
	// TODO: register forked Kubeshark dissectors here
	// r.Register(&HTTPDissector{})
	// r.Register(&GRPCDissector{})
	// r.Register(&DNSDissector{})
	// r.Register(&KafkaDissector{})
	// r.Register(&RedisDissector{})
	return r
}

// Register adds a dissector to the registry.
func (r *Registry) Register(d Dissector) {
	r.all = append(r.all, d)
	for _, p := range d.Ports() {
		r.byPort[p] = d
	}
}

// Lookup returns the dissector for a given port, or nil.
func (r *Registry) Lookup(port int) Dissector {
	return r.byPort[port]
}

// All returns all registered dissectors.
func (r *Registry) All() []Dissector {
	return r.all
}
