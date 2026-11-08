// Package api implements the socket.io server that the dashboard UI connects to.
// This is the hub → UI contract — same as the traffic-simulator's, but
// backed by real enriched data.
//
// Events emitted to UI:
//   - cluster:topology   — sent on connect, contains all known services + edges
//   - traffic:entry      — sent for every captured entry (post-enrichment, post-redaction)
//   - stats:snapshot     — sent every 5s, contains counts/rates for the header pills
//
// Events received from UI:
//   - filter:set         — UI informs hub of current display filter (for stats)
//   - replay:request     — UI requests entries in a time range (for time machine)
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"nhooyr.io/websocket"
)

// Server is the socket.io-compatible WebSocket server.
// We use raw websockets instead of socket.io for the hub→UI connection to
// avoid the socket.io dependency in the Go backend. The frontend's
// socket.io-client falls back to plain WebSocket transport.
//
// EVENT FORMAT: messages are JSON-encoded with a {event, data} envelope.
// The frontend adapter in src/store/traffic-store.ts understands this format.
type Server struct {
	addr     string
	entries  chan *UIEntry
	topology func() *UITopology

	// connected clients (for fan-out)
	mu      sync.RWMutex
	clients map[*websocket.Conn]struct{}
}

// UIEntry is the JSON shape the dashboard's TrafficEntry TypeScript type expects.
// This MUST stay in sync with src/lib/traffic-types.ts.
type UIEntry struct {
	ID          string    `json:"id"`
	Timestamp   int64     `json:"timestamp"`
	Protocol    string    `json:"protocol"`
	TLS         bool      `json:"tls"`
	Source      UIEndpoint `json:"source"`
	Destination UIEndpoint `json:"destination"`
	Method      string    `json:"method,omitempty"`
	Path        string    `json:"path,omitempty"`
	Status      int32     `json:"status,omitempty"`
	LatencyMs   int32     `json:"latencyMs"`
	RequestID   string    `json:"requestId,omitempty"`
	IsRetry     bool      `json:"isRetry,omitempty"`
	Request     UIPayload `json:"request"`
	Response    UIPayload `json:"response"`
	GrpcStatus  string    `json:"grpcStatus,omitempty"`
	DNSQuery    string    `json:"dnsQuery,omitempty"`
	DNSType     string    `json:"dnsType,omitempty"`
	DNSRcode    string    `json:"dnsRcode,omitempty"`
	DNSAnswer   []string  `json:"dnsAnswer,omitempty"`
	DNSTtl      int32     `json:"dnsTtl,omitempty"`
}

type UIEndpoint struct {
	NS      string `json:"ns"`
	Pod     string `json:"pod"`
	Service string `json:"service"`
	IP      string `json:"ip"`
	Port    int32  `json:"port"`
}

type UIPayload struct {
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body,omitempty"`
	Size    int32             `json:"size"`
}

// UITopology matches the ClusterTopology TypeScript type.
type UITopology struct {
	Services []UIService `json:"services"`
	Edges    []UIEdge    `json:"edges"`
}

type UIService struct {
	Name     string `json:"name"`
	NS       string `json:"ns"`
	Kind     string `json:"kind"`
	IP       string `json:"ip"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

type UIEdge struct {
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	Proto  string `json:"proto"`
	Weight int    `json:"weight"`
}

// message envelope for the wire
type message struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// New creates a Server bound to addr (e.g., ":3030").
// topologyFn is called on each new client connection to emit the current topology.
func New(addr string, topologyFn func() *UITopology) *Server {
	return &Server{
		addr:     addr,
		entries:  make(chan *UIEntry, 4096),
		topology: topologyFn,
		clients:  make(map[*websocket.Conn]struct{}),
	}
}

// Submit queues an enriched entry for fan-out to all connected dashboards.
// Non-blocking — if the queue is full, the entry is dropped (sampling).
func (s *Server) Submit(e *UIEntry) {
	select {
	case s.entries <- e:
	default:
		log.Warn().Msg("api: submit queue full, dropping entry")
	}
}

// Start runs the WebSocket server. Blocks until ctx is canceled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	// fan-out goroutine
	go s.fanout(ctx)

	log.Info().Str("addr", s.addr).Msg("api: WebSocket server starting")
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	return srv.ListenAndServe()
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Accept OPTIONS for CORS preflight (browsers send this before WS upgrade)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"}, // tighten in production
	})
	if err != nil {
		log.Warn().Err(err).Msg("api: ws accept failed")
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// register client
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
	}()

	log.Info().Str("remote", r.RemoteAddr).Msg("api: dashboard connected")

	// send topology on connect
	if topo := s.topology(); topo != nil {
		s.send(c, "cluster:topology", topo)
	}

	// send backlog of recent entries (last 200, newest first)
	// TODO: pull from store

	// read loop (handle filter:set, replay:request from UI)
	ctx := r.Context()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			log.Debug().Err(err).Msg("api: ws read closed")
			return
		}
		var msg message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Event {
		case "filter:set":
			// UI informs us of display filter — used for stats only
			log.Debug().Str("filter", string(msg.Data)).Msg("api: filter set")
		case "replay:request":
			// UI requests historical entries in a time range
			// TODO: query store.Range and emit as traffic:entry
		case "ping":
			s.send(c, "pong", nil)
		}
	}
}

// fanout reads from the entries channel and writes to every connected client.
func (s *Server) fanout(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-s.entries:
			s.mu.RLock()
			clients := make([]*websocket.Conn, 0, len(s.clients))
			for c := range s.clients {
				clients = append(clients, c)
			}
			s.mu.RUnlock()

			for _, c := range clients {
				if err := s.send(c, "traffic:entry", e); err != nil {
					// client likely disconnected
					s.mu.Lock()
					delete(s.clients, c)
					s.mu.Unlock()
					c.Close(websocket.StatusNormalClosure, "")
				}
			}
		}
	}
}

func (s *Server) send(c *websocket.Conn, event string, data any) error {
	var rawData json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		rawData = b
	}
	msg := message{Event: event, Data: rawData}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, payload)
}
