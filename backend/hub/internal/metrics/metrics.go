// Package metrics defines Prometheus collectors for the Bullseye hub.
//
// Exposed metrics:
//   bullseye_hub_entries_received_total{node}      counter
//   bullseye_hub_entries_dropped_total            counter
//   bullseye_hub_entries_persisted_total          counter
//   bullseye_hub_redactions_total{type}           counter
//   bullseye_hub_websocket_clients                gauge
//   bullseye_hub_websocket_messages_sent_total    counter
//   bullseye_hub_grpc_streams_active              gauge
//   bullseye_hub_store_entries                    gauge
//   bullseye_hub_store_sweep_deleted_total        counter
//   bullseye_hub_uptime_seconds                   gauge
package metrics

import (
        "net/http"
        "sync/atomic"
        "time"

        "github.com/prometheus/client_golang/prometheus"
        "github.com/prometheus/client_golang/prometheus/promhttp"
        "github.com/rs/zerolog/log"
)

// Registry holds all hub metrics.
type Registry struct {
        EntriesReceived    *prometheus.CounterVec
        EntriesDropped     prometheus.Counter
        EntriesPersisted   prometheus.Counter
        Redactions         *prometheus.CounterVec
        WSClients          prometheus.Gauge
        WSMessagesSent     prometheus.Counter
        GRPCStreamsActive  prometheus.Gauge
        StoreEntries       prometheus.Gauge
        StoreSweepDeleted  prometheus.Counter
        Uptime             prometheus.Gauge

        startTime time.Time
}

var (
        registry *Registry
        startTime atomic.Int64
)

// New creates and registers a Registry with the default prometheus registry.
func New() *Registry {
        r := &Registry{
                EntriesReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
                        Name: "bullseye_hub_entries_received_total",
                        Help: "Total entries received from sniffers.",
                }, []string{"node"}),
                EntriesDropped: prometheus.NewCounter(prometheus.CounterOpts{
                        Name: "bullseye_hub_entries_dropped_total",
                        Help: "Entries dropped due to full queues or processing errors.",
                }),
                EntriesPersisted: prometheus.NewCounter(prometheus.CounterOpts{
                        Name: "bullseye_hub_entries_persisted_total",
                        Help: "Entries successfully persisted to BoltDB.",
                }),
                Redactions: prometheus.NewCounterVec(prometheus.CounterOpts{
                        Name: "bullseye_hub_redactions_total",
                        Help: "PII redactions applied.",
                }, []string{"type"}), // type: "header" or "body"
                WSClients: prometheus.NewGauge(prometheus.GaugeOpts{
                        Name: "bullseye_hub_websocket_clients",
                        Help: "Currently connected WebSocket clients.",
                }),
                WSMessagesSent: prometheus.NewCounter(prometheus.CounterOpts{
                        Name: "bullseye_hub_websocket_messages_sent_total",
                        Help: "Total WebSocket messages sent to dashboards.",
                }),
                GRPCStreamsActive: prometheus.NewGauge(prometheus.GaugeOpts{
                        Name: "bullseye_hub_grpc_streams_active",
                        Help: "Currently active gRPC streams from sniffers.",
                }),
                StoreEntries: prometheus.NewGauge(prometheus.GaugeOpts{
                        Name: "bullseye_hub_store_entries",
                        Help: "Total entries currently in BoltDB.",
                }),
                StoreSweepDeleted: prometheus.NewCounter(prometheus.CounterOpts{
                        Name: "bullseye_hub_store_sweep_deleted_total",
                        Help: "Entries deleted by TTL sweep.",
                }),
                Uptime: prometheus.NewGauge(prometheus.GaugeOpts{
                        Name: "bullseye_hub_uptime_seconds",
                        Help: "Hub uptime in seconds.",
                }),
        }

        prometheus.MustRegister(
                r.EntriesReceived, r.EntriesDropped, r.EntriesPersisted,
                r.Redactions, r.WSClients, r.WSMessagesSent,
                r.GRPCStreamsActive, r.StoreEntries, r.StoreSweepDeleted, r.Uptime,
        )

        startTime.Store(time.Now().Unix())
        registry = r
        return r
}

// Handler returns the HTTP handler for /metrics.
func Handler() http.Handler {
        return promhttp.Handler()
}

// Get returns the singleton registry.
func Get() *Registry {
        return registry
}

// UpdateUptime refreshes the uptime gauge.
func UpdateUptime() {
        if registry == nil {
                return
        }
        start := startTime.Load()
        registry.Uptime.Set(float64(time.Now().Unix() - start))
}

// StartUptimeUpdater runs a goroutine that updates uptime every 15s.
func StartUptimeUpdater(stop <-chan struct{}) {
        go func() {
                ticker := time.NewTicker(15 * time.Second)
                defer ticker.Stop()
                for {
                        select {
                        case <-stop:
                                return
                        case <-ticker.C:
                                UpdateUptime()
                        }
                }
        }()
}

// Log helper for metrics init
func init() {
        log.Info().Msg("metrics: Prometheus registry initialized")
}

// --- methods to satisfy api.MetricsRegistry interface ---

// IncEntriesReceived increments the entries-received counter for a node.
func (r *Registry) IncEntriesReceived(node string) {
        if r == nil {
                return
        }
        r.EntriesReceived.WithLabelValues(node).Inc()
}

// IncEntriesDropped increments the dropped counter.
func (r *Registry) IncEntriesDropped() {
        if r == nil {
                return
        }
        r.EntriesDropped.Inc()
}

// IncEntriesPersisted increments the persisted counter.
func (r *Registry) IncEntriesPersisted() {
        if r == nil {
                return
        }
        r.EntriesPersisted.Inc()
}

// IncRedactions increments the redactions counter by type.
func (r *Registry) IncRedactions(rtype string) {
        if r == nil {
                return
        }
        r.Redactions.WithLabelValues(rtype).Inc()
}

// IncWSMessagesSent increments the ws-messages-sent counter.
func (r *Registry) IncWSMessagesSent() {
        if r == nil {
                return
        }
        r.WSMessagesSent.Inc()
}

// IncWSDropped is a no-op for now (uses IncEntriesDropped).
func (r *Registry) IncWSDropped() {
        if r == nil {
                return
        }
        r.EntriesDropped.Inc()
}

// SetWSClients sets the active websocket client gauge.
func (r *Registry) SetWSClients(n int) {
        if r == nil {
                return
        }
        r.WSClients.Set(float64(n))
}
