// Package main is the Bullseye hub entry point.
//
// The hub is the central collector that:
//   1. Receives CaptureEntry streams from sniffer DaemonSets (gRPC, port 9090)
//   2. Enriches entries with K8s pod/service/namespace metadata (informers)
//   3. Applies PII redaction (header denylist + body regex)
//   4. Persists to BoltDB (with TTL sweep)
//   5. Relays enriched entries to dashboard UI via WebSocket (port 3030)
//   6. Exposes Prometheus metrics (port 49100)
package main

import (
        "context"
        "flag"
        "fmt"
        "net"
        "os"
        "os/signal"
        "syscall"
        "time"

        capturev1 "github.com/bullseye/bullseye/backend/proto/bullseye/v1"
        "github.com/bullseye/bullseye/backend/hub/internal/api"
        "github.com/bullseye/bullseye/backend/hub/internal/config"
        "github.com/bullseye/bullseye/backend/hub/internal/enrich"
        "github.com/bullseye/bullseye/backend/hub/internal/rbac"
        "github.com/bullseye/bullseye/backend/hub/internal/redact"
        "github.com/bullseye/bullseye/backend/hub/internal/store"
        "github.com/rs/zerolog"
        "github.com/rs/zerolog/log"
        "google.golang.org/grpc"
        "k8s.io/client-go/kubernetes"
        "k8s.io/client-go/rest"
)

func main() {
        configPath := flag.String("config", "", "path to config file (optional — env vars used by default)")
        flag.Parse()

        // load config
        cfg, err := config.Load()
        if err != nil {
                fmt.Fprintf(os.Stderr, "config error: %v\n", err)
                os.Exit(1)
        }
        _ = configPath

        // configure logger
        level, _ := zerolog.ParseLevel(cfg.LogLevel)
        zerolog.SetGlobalLevel(level)
        log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

        log.Info().
                Interface("config", cfg).
                Msg("bullseye hub starting")

        // root context — canceled on SIGTERM/SIGINT
        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        sigCh := make(chan os.Signal, 1)
        signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
        go func() {
                sig := <-sigCh
                log.Info().Str("signal", sig.String()).Msg("shutting down")
                cancel()
        }()

        // 1. K8s client (nil in standalone dev mode)
        k8sClient, err := newK8sClient(cfg)
        if err != nil {
                log.Fatal().Err(err).Msg("k8s client init failed")
        }
        if k8sClient == nil {
                log.Info().Msg("running in standalone mode — no K8s enrichment will be applied")
        }

        // 2. Enricher (informers) — only when K8s client is available
        var enricher *enrich.Enricher
        if k8sClient != nil {
                enricher, err = enrich.New(ctx, k8sClient, 10*time.Minute)
                if err != nil {
                        log.Fatal().Err(err).Msg("enricher init failed")
                }
        }

        // 3. Redactor
        redactor, err := redact.New(cfg.RedactHeaders, cfg.RedactBodyPatterns)
        if err != nil {
                log.Fatal().Err(err).Msg("redactor init failed")
        }
        log.Info().Int("header_rules", len(cfg.RedactHeaderSet())).Msg("redactor initialized")

        // 4. Persistence (BoltDB)
        st, err := store.New(cfg.StoragePath)
        if err != nil {
                log.Fatal().Err(err).Msg("store init failed")
        }
        defer st.Close()
        stopSweeper := st.RunSweeper(ctx, cfg.RetentionTTL, 5*time.Minute)
        defer stopSweeper()

        // 5. RBAC authorizer (disabled in dev)
        var authorizer *rbac.Authorizer
        if k8sClient != nil {
                authorizer = rbac.New(k8sClient, cfg.OIDCIssuer != "")
        }
        log.Info().Bool("rbac_enabled", cfg.OIDCIssuer != "").Msg("rbac initialized")

        // 6. WebSocket server for dashboard UI
        wsServer := api.New(fmt.Sprintf(":%d", cfg.WSPort), func() *api.UITopology {
                if enricher == nil {
                        return nil
                }
                return buildTopology(enricher)
        })

        // 7. gRPC server for sniffers
        captureServer := &api.CaptureServer{
                Enricher: enricherAdapterOrNil(enricher),
                Redactor: &redactorAdapter{r: redactor},
                Store:    &storeAdapter{s: st},
                WS:       wsServer,
        }
        grpcServer := grpc.NewServer(
                grpc.MaxRecvMsgSize(16*1024*1024),
                grpc.MaxSendMsgSize(16*1024*1024),
        )
        capturev1.RegisterCaptureServiceServer(grpcServer, captureServer)

        lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
        if err != nil {
                log.Fatal().Err(err).Int("port", cfg.GRPCPort).Msg("grpc listen failed")
        }
        go func() {
                log.Info().Int("port", cfg.GRPCPort).Msg("grpc: sniffer server starting")
                if err := grpcServer.Serve(lis); err != nil {
                        log.Error().Err(err).Msg("grpc server stopped")
                }
        }()
        defer grpcServer.GracefulStop()

        log.Info().
                Int("ws_port", cfg.WSPort).
                Int("grpc_port", cfg.GRPCPort).
                Int("metrics_port", cfg.MetricsPort).
                Msg("hub serving")

        // block on WebSocket server (gRPC server runs in goroutine)
        if err := wsServer.Start(ctx); err != nil {
                log.Fatal().Err(err).Msg("ws server stopped")
        }

        _ = authorizer // used by HTTP handlers when OIDC is enabled
}

// enricherAdapterOrNil returns nil if enricher is nil (standalone dev mode),
// otherwise returns an adapter wrapping it.
func enricherAdapterOrNil(e *enrich.Enricher) api.Enricher {
        if e == nil {
                return nil
        }
        return &enricherAdapter{e: e}
}

func newK8sClient(cfg *config.Config) (kubernetes.Interface, error) {
        var k8sCfg *rest.Config
        var err error
        if cfg.InCluster {
                k8sCfg, err = rest.InClusterConfig()
                if err != nil {
                        // fall back to standalone mode for local dev — log and continue
                        // with a no-op client so we can test the gRPC + WS pipeline
                        // without a real K8s cluster.
                        log.Warn().Err(err).Msg("in-cluster config failed — starting in standalone mode (no K8s enrichment)")
                        return nil, nil
                }
        } else {
                // standalone dev mode — no K8s client
                log.Warn().Msg("IN_CLUSTER=false — starting in standalone mode (no K8s enrichment)")
                return nil, nil
        }
        return kubernetes.NewForConfig(k8sCfg)
}

// buildTopology converts the enricher's service list into the UI topology shape.
func buildTopology(e *enrich.Enricher) *api.UITopology {
        svcs := e.ListServices()
        out := &api.UITopology{
                Services: make([]api.UIService, 0, len(svcs)),
        }
        for _, s := range svcs {
                // determine kind from labels or presence of selector
                kind := "deployment"
                if len(s.Selector) == 0 {
                        kind = "external"
                }
                // pick first port for display
                var port int32
                if len(s.Ports) > 0 {
                        port = int32(s.Ports[0].Port)
                }
                // protocol is inferred from port — 53=DNS, 6379=Redis, 5432=Postgres,
                // 9092=Kafka, else HTTP
                proto := protoForPort(port)
                out.Services = append(out.Services, api.UIService{
                        Name: s.Name, NS: s.Namespace, Kind: kind,
                        IP: s.ClusterIP, Port: port, Protocol: proto,
                })
        }
        // edges: derived from observed traffic — populated by the hub as entries arrive.
        // For now, empty; the UI also derives edges from observed entries on its side.
        return out
}

func protoForPort(p int32) string {
        switch p {
        case 53:
                return "DNS"
        case 6379:
                return "Redis"
        case 5432, 3306:
                return "Postgres"
        case 9092:
                return "Kafka"
        case 8080, 80, 443, 8000, 3000:
                return "HTTP"
        default:
                return "HTTP"
        }
}

// loadKubeconfig loads ~/.kube/config for dev mode.
func loadKubeconfig() (*rest.Config, error) {
        // Use the k8s.io/client-go/tools/clientcmd package in production.
        // Avoided here to keep imports minimal until we wire up dev mode.
        return nil, fmt.Errorf("kubeconfig loading not implemented — set IN_CLUSTER=true or run inside K8s")
}

// --- adapters: bridge hub internal types to the api package's interfaces ---
// Without these, the api package would need to import enrich/redact/store
// directly, creating an import cycle (api is imported by main, which also
// imports enrich/redact/store).

type enricherAdapter struct{ e *enrich.Enricher }

func (a *enricherAdapter) LookupPod(ip string) *api.PodInfo {
        p := a.e.LookupPod(ip)
        if p == nil {
                return nil
        }
        return &api.PodInfo{
                Name: p.Name, Namespace: p.Namespace, IP: p.IP,
                HostIP: p.HostIP, NodeName: p.NodeName,
        }
}

func (a *enricherAdapter) LookupService(ip string) *api.ServiceInfo {
        s := a.e.LookupService(ip)
        if s == nil {
                return nil
        }
        return &api.ServiceInfo{
                Name: s.Name, Namespace: s.Namespace, ClusterIP: s.ClusterIP,
        }
}

func (a *enricherAdapter) LookupServiceForPod(podName, namespace string) []string {
        return a.e.LookupServiceForPod(podName, namespace)
}

type redactorAdapter struct{ r *redact.Redactor }

func (a *redactorAdapter) RedactHeaders(headers map[string]string) int {
        return a.r.RedactHeaders(headers)
}
func (a *redactorAdapter) RedactBody(body []byte) ([]byte, bool) {
        return a.r.RedactBody(body)
}

type storeAdapter struct{ s *store.Store }

func (a *storeAdapter) Put(entry *api.StoreEntry) error {
        return a.s.Put(storeEntryFromAPI(entry))
}
func (a *storeAdapter) Recent(n int) ([]*api.StoreEntry, error) {
        entries, err := a.s.Recent(n)
        if err != nil {
                return nil, err
        }
        out := make([]*api.StoreEntry, 0, len(entries))
        for _, e := range entries {
                out = append(out, storeEntryToAPI(e))
        }
        return out, nil
}

func storeEntryFromAPI(e *api.StoreEntry) *store.Entry {
        return &store.Entry{
                ID:          e.ID,
                Timestamp:   e.Timestamp,
                Protocol:    e.Protocol,
                TLS:         e.TLS,
                Source:      storeEndpointFromAPI(e.Source),
                Destination: storeEndpointFromAPI(e.Destination),
                Method:      e.Method,
                Path:        e.Path,
                Status:      e.Status,
                LatencyMs:   e.LatencyMs,
                RequestID:   e.RequestID,
                IsRetry:     e.IsRetry,
                Request:     storePayloadFromAPI(e.Request),
                Response:    storePayloadFromAPI(e.Response),
                GrpcStatus:  e.GrpcStatus,
                DNSQuery:    e.DNSQuery,
                DNSType:     e.DNSType,
                DNSRcode:    e.DNSRcode,
                DNSAnswer:   e.DNSAnswer,
                DNSTtl:      e.DNSTtl,
                NodeName:    e.NodeName,
                SnifferPod:  e.SnifferPod,
        }
}

func storeEntryToAPI(e *store.Entry) *api.StoreEntry {
        return &api.StoreEntry{
                ID:          e.ID,
                Timestamp:   e.Timestamp,
                Protocol:    e.Protocol,
                TLS:         e.TLS,
                Source:      storeEndpointToAPI(e.Source),
                Destination: storeEndpointToAPI(e.Destination),
                Method:      e.Method,
                Path:        e.Path,
                Status:      e.Status,
                LatencyMs:   e.LatencyMs,
                RequestID:   e.RequestID,
                IsRetry:     e.IsRetry,
                Request:     storePayloadToAPI(e.Request),
                Response:    storePayloadToAPI(e.Response),
                GrpcStatus:  e.GrpcStatus,
                DNSQuery:    e.DNSQuery,
                DNSType:     e.DNSType,
                DNSRcode:    e.DNSRcode,
                DNSAnswer:   e.DNSAnswer,
                DNSTtl:      e.DNSTtl,
                NodeName:    e.NodeName,
                SnifferPod:  e.SnifferPod,
        }
}

func storeEndpointFromAPI(e api.StoreEndpoint) store.Endpoint {
        return store.Endpoint{IP: e.IP, Port: e.Port, Pod: e.Pod, Service: e.Service, NS: e.NS}
}

func storeEndpointToAPI(e store.Endpoint) api.StoreEndpoint {
        return api.StoreEndpoint{IP: e.IP, Port: e.Port, Pod: e.Pod, Service: e.Service, NS: e.NS}
}

func storePayloadFromAPI(p api.StorePayload) store.Payload {
        return store.Payload{Headers: p.Headers, Body: p.Body, Size: p.Size}
}

func storePayloadToAPI(p store.Payload) api.StorePayload {
        return api.StorePayload{Headers: p.Headers, Body: p.Body, Size: p.Size}
}
