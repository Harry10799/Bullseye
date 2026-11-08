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
        "os"
        "os/signal"
        "syscall"
        "time"

        "github.com/bullseye/bullseye/backend/hub/internal/api"
        "github.com/bullseye/bullseye/backend/hub/internal/config"
        "github.com/bullseye/bullseye/backend/hub/internal/enrich"
        "github.com/bullseye/bullseye/backend/hub/internal/rbac"
        "github.com/bullseye/bullseye/backend/hub/internal/redact"
        "github.com/bullseye/bullseye/backend/hub/internal/store"
        "github.com/rs/zerolog"
        "github.com/rs/zerolog/log"
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

        // 1. K8s client
        k8sClient, err := newK8sClient(cfg)
        if err != nil {
                log.Fatal().Err(err).Msg("k8s client init failed")
        }
        log.Info().Msg("k8s client initialized")

        // 2. Enricher (informers)
        enricher, err := enrich.New(ctx, k8sClient, 10*time.Minute)
        if err != nil {
                log.Fatal().Err(err).Msg("enricher init failed")
        }

        // 3. Redactor
        redactor, err := redact.New(cfg.RedactHeaders, cfg.RedactBodyPatterns)
        if err != nil {
                log.Fatal().Err(err).Msg("redactor init failed")
        }
        log.Info().Int("header_rules", len(cfg.RedactHeaderSet())).Msg("redactor initialized")
        _ = redactor // used by gRPC CaptureServer once proto is generated

        // 4. Persistence (BoltDB)
        st, err := store.New(cfg.StoragePath)
        if err != nil {
                log.Fatal().Err(err).Msg("store init failed")
        }
        defer st.Close()
        stopSweeper := st.RunSweeper(ctx, cfg.RetentionTTL, 5*time.Minute)
        defer stopSweeper()

        // 5. RBAC authorizer (disabled in dev)
        authorizer := rbac.New(k8sClient, cfg.OIDCIssuer != "")
        log.Info().Bool("rbac_enabled", cfg.OIDCIssuer != "").Msg("rbac initialized")

        // 6. WebSocket server for dashboard UI
        wsServer := api.New(fmt.Sprintf(":%d", cfg.WSPort), func() *api.UITopology {
                return buildTopology(enricher)
        })

        // 7. gRPC server for sniffers
        // TODO: after gen-proto.sh, register the real CaptureServiceServer
        // captureServer := &api.CaptureServer{
        //     Enricher: enricher, Redactor: redactor, Store: st, WS: wsServer,
        // }
        // grpcServer := grpc.NewServer()
        // capturev1.RegisterCaptureServiceServer(grpcServer, captureServer)
        // lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
        // go grpcServer.Serve(lis)

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

func newK8sClient(cfg *config.Config) (kubernetes.Interface, error) {
        var k8sCfg *rest.Config
        var err error
        if cfg.InCluster {
                k8sCfg, err = rest.InClusterConfig()
                if err != nil {
                        return nil, fmt.Errorf("in-cluster config: %w (are we running in K8s?)", err)
                }
        } else {
                // dev mode — fall back to kubeconfig
                k8sCfg, err = loadKubeconfig()
                if err != nil {
                        return nil, fmt.Errorf("kubeconfig: %w", err)
                }
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
