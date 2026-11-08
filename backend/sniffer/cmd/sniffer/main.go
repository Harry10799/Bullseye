// Package main is the Bullseye sniffer entry point.
//
// The sniffer runs as a DaemonSet on every node. It captures packets via
// AF_PACKET (or eBPF if enabled), dissects them into L7 messages, optionally
// decrypts TLS via the tracer sidecar, and streams CaptureEntry messages
// to the hub via gRPC.
//
// Licensing: this binary is Apache-2.0. The TLS tracer is GPL-3.0 and runs
// as a separate sidecar container — see internal/tls/decryptor.go.
package main

import (
        "context"
        "flag"
        "fmt"
        "os"
        "os/signal"
        "syscall"
        "time"

        capturev1 "github.com/bullseye/bullseye/backend/proto/bullseye/v1"
        "github.com/bullseye/bullseye/backend/sniffer/internal/capture"
        "github.com/bullseye/bullseye/backend/sniffer/internal/tls"
        "github.com/rs/zerolog"
        "github.com/rs/zerolog/log"
        "google.golang.org/grpc"
        "google.golang.org/grpc/credentials/insecure"
)

func main() {
        hubAddr := flag.String("hub", "bullseye-hub:9090", "hub gRPC address")
        iface := flag.String("iface", "any", "network interface to capture on")
        backend := flag.String("backend", "afpacket", "capture backend: afpacket | ebpf")
        tlsTracer := flag.String("tls-tracer", "localhost:9091", "TLS tracer sidecar address (empty to disable)")
        nodeName := flag.String("node", "", "K8s node name (auto-detected from env)")
        rate := flag.Float64("rate", 0, "if >0, use fake-sniffer mode at this rate (for testing)")
        flag.Parse()

        zerolog.SetGlobalLevel(zerolog.InfoLevel)
        log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

        if *nodeName == "" {
                *nodeName = os.Getenv("NODE_NAME")
        }
        if *nodeName == "" {
                *nodeName = "standalone"
        }

        log.Info().
                Str("hub", *hubAddr).
                Str("iface", *iface).
                Str("backend", *backend).
                Str("node", *nodeName).
                Float64("fake-rate", *rate).
                Msg("bullseye sniffer starting")

        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        sigCh := make(chan os.Signal, 1)
        signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
        go func() {
                <-sigCh
                log.Info().Msg("shutting down")
                cancel()
        }()

        // 1. Connect to hub via gRPC
        conn, err := grpc.NewClient(*hubAddr,
                grpc.WithTransportCredentials(insecure.NewCredentials()),
                grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(16*1024*1024)),
        )
        if err != nil {
                log.Fatal().Err(err).Msg("dial hub failed")
        }
        defer conn.Close()

        client := capturev1.NewCaptureServiceClient(conn)
        stream, err := openStream(ctx, client)
        if err != nil {
                if ctx.Err() == nil {
                        log.Error().Err(err).Msg("open stream failed")
                }
                return
        }

        // ack reader goroutine
        go func() {
                for {
                        ack, err := stream.Recv()
                        if err != nil {
                                return
                        }
                        log.Debug().Str("ack_last", ack.LastEntryId).
                                Int32("ack_count", ack.EntriesReceived).
                                Msg("got ack")
                }
        }()

        // entry channel — dissectors push here, emitter drains to gRPC
        entryCh := make(chan *capturev1.CaptureEntry, 4096)

        // emitter goroutine — drains entryCh and sends to hub via gRPC
        emitterDone := make(chan struct{})
        go func() {
                defer close(emitterDone)
                for entry := range entryCh {
                        if err := stream.Send(entry); err != nil {
                                log.Warn().Err(err).Msg("emit: send failed")
                                return
                        }
                }
        }()

        // 2. TLS decryptor (optional sidecar)
        if *tlsTracer != "" {
                dec := tls.New(*tlsTracer)
                go dec.Start(ctx)
        }

        // 3. Capture + packet processing loop
        if *rate > 0 {
                // fake-sniffer mode — use synthetic entries for testing
                log.Info().Float64("rate", *rate).Msg("running in fake-sniffer mode (synthetic entries)")
                runFakeMode(ctx, *rate, *nodeName, entryCh)
        } else {
                // production mode — real AF_PACKET capture
                cap, err := capture.New(capture.Config{
                        Backend:   *backend,
                        Interface: *iface,
                        Promisc:   true,
                        SnapLen:   65535,
                        BPFFilter: "tcp or udp port 80 or port 443 or port 53 or port 8080 or port 9090 or port 6379 or port 5432 or port 9092",
                })
                if err != nil {
                        log.Warn().Err(err).Msg("capture init failed — falling back to fake-sniffer mode at 5/sec")
                        runFakeMode(ctx, 5.0, *nodeName, entryCh)
                } else {
                        processor := newPacketProcessor(entryCh, *nodeName)
                        go processor.runGC(ctx.Done())
                        if err := cap.Start(ctx); err != nil {
                                log.Fatal().Err(err).Msg("capture start failed")
                        }
                        log.Info().Msg("sniffer ready — capturing real packets")

                        if err := processCapturedPackets(ctx, cap.Packets(), processor); err != nil {
                                log.Error().Err(err).Msg("capture packet processing stopped")
                                cancel()
                        }
                        _ = cap.Stop()
                }
        }

        // wait for emitter to drain
        close(entryCh)
        <-emitterDone
        log.Info().Msg("sniffer stopped")
}

func openStream(ctx context.Context, client capturev1.CaptureServiceClient) (capturev1.CaptureService_StreamEntriesClient, error) {
	const retryInterval = 3 * time.Second

	for attempt := 1; ; attempt++ {
		stream, err := client.StreamEntries(ctx)
		if err == nil {
			if attempt > 1 {
				log.Info().Int("attempts", attempt).Msg("connected to hub")
			}
			return stream, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		log.Warn().Err(err).Int("attempt", attempt).Dur("retry_in", retryInterval).
			Msg("open stream failed; retrying")

		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// runFakeMode generates synthetic entries at the given rate (for testing).
// This is a simplified version of fake-sniffer/main.go.
func runFakeMode(ctx context.Context, rate float64, nodeName string, out chan<- *capturev1.CaptureEntry) {
        ticker := time.NewTicker(time.Duration(float64(time.Second) / rate))
        defer ticker.Stop()

        methods := []string{"GET", "POST", "PUT", "DELETE"}
        paths := []string{"/api/users", "/api/orders", "/api/products", "/v1/charges", "/v1/refunds"}
        statuses := []int32{200, 200, 200, 201, 404, 500, 503}

        for {
                select {
                case <-ctx.Done():
                        return
                case <-ticker.C:
                        now := time.Now().UnixMilli()
                        entry := &capturev1.CaptureEntry{
                                Id:        fmt.Sprintf("pkt_%d_%d", now, time.Now().UnixNano()%1000000),
                                Timestamp: now,
                                Protocol:  capturev1.Protocol_HTTP,
                                Source:    &capturev1.Endpoint{Ip: "10.0.1.1", Port: 8080},
                                Destination: &capturev1.Endpoint{Ip: "10.0.2.2", Port: 8080},
                                Method:    methods[now%int64(len(methods))],
                                Path:      paths[now%int64(len(paths))],
                                Status:    statuses[now%int64(len(statuses))],
                                LatencyMs: int32(10 + now%500),
                                NodeName:  nodeName,
                                SnifferPod: fmt.Sprintf("bullseye-sniffer-%s", nodeName),
                                Request: &capturev1.Payload{
                                        Headers: map[string]string{
                                                "content-type":  "application/json",
                                                "authorization": "Bearer eyJhbGciOiJIUzI1NiJ9.test.test",
                                        },
                                        Body: []byte(`{"ok":true}`),
                                        Size: 12,
                                },
                                Response: &capturev1.Payload{
                                        Headers: map[string]string{"content-type": "application/json"},
                                        Body:    []byte(`{"ok":true}`),
                                        Size:    12,
                                },
                        }
                        select {
                        case out <- entry:
                        default:
                        }
                }
        }
}
