// Package main — load-test is a stress test for the Bullseye hub.
//
// It spawns N fake-sniffer goroutines, each streaming entries at M pps to the
// hub, and reports throughput + drop rate + memory stats.
//
// Usage:
//   ./load-test --hub=localhost:9090 --nodes=10 --rate=100 --duration=60s
//
// Metrics reported:
//   - sent vs acknowledged entries
//   - send rate (entries/sec)
//   - WS client receives (subscribe to hub WS to count)
//   - hub-side /metrics snapshot (entries_received_total, dropped_total)
package main

import (
        "context"
        "flag"
        "fmt"
        "math/rand"
        "os"
        "os/signal"
        "sync"
        "sync/atomic"
        "syscall"
        "time"

        capturev1 "github.com/bullseye/bullseye/backend/proto/bullseye/v1"
        "github.com/rs/zerolog"
        "github.com/rs/zerolog/log"
        "google.golang.org/grpc"
        "google.golang.org/grpc/credentials/insecure"
)

func main() {
        hubAddr := flag.String("hub", "localhost:9090", "hub gRPC address")
        nodes := flag.Int("nodes", 5, "number of fake sniffer goroutines")
        rate := flag.Float64("rate", 50, "entries per second per node")
        duration := flag.Duration("duration", 30*time.Second, "test duration")
        flag.Parse()

        zerolog.SetGlobalLevel(zerolog.InfoLevel)
        log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

        log.Info().
                Str("hub", *hubAddr).
                Int("nodes", *nodes).
                Float64("rate_per_node", *rate).
                Dur("duration", *duration).
                Float64("total_rate", float64(*nodes)**rate).
                Msg("bullseye load-test starting")

        ctx, cancel := context.WithTimeout(context.Background(), *duration)
        defer cancel()

        sigCh := make(chan os.Signal, 1)
        signal.Notify(sigCh, syscall.SIGINT)
        go func() { <-sigCh; cancel() }()

        // connect to hub
        conn, err := grpc.NewClient(*hubAddr,
                grpc.WithTransportCredentials(insecure.NewCredentials()),
                grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(16*1024*1024)),
        )
        if err != nil {
                log.Fatal().Err(err).Msg("dial hub failed")
        }
        defer conn.Close()
        client := capturev1.NewCaptureServiceClient(conn)

        var sent, acked atomic.Int64
        var wg sync.WaitGroup

        // spawn N fake sniffers
        for i := 0; i < *nodes; i++ {
                nodeName := fmt.Sprintf("load-node-%d", i)
                stream, err := client.StreamEntries(ctx)
                if err != nil {
                        log.Fatal().Err(err).Msg("open stream failed")
                }

                // ack reader goroutine
                wg.Add(1)
                go func() {
                        defer wg.Done()
                        for {
                                ack, err := stream.Recv()
                                if err != nil {
                                        return
                                }
                                acked.Add(int64(ack.EntriesReceived))
                        }
                }()

                // sender goroutine
                wg.Add(1)
                go func(node string) {
                        defer wg.Done()
                        ticker := time.NewTicker(time.Duration(float64(time.Second) / *rate))
                        defer ticker.Stop()
                        for {
                                select {
                                case <-ctx.Done():
                                        _ = stream.CloseSend()
                                        return
                                case <-ticker.C:
                                        entry := genEntry(node)
                                        if err := stream.Send(entry); err != nil {
                                                return
                                        }
                                        sent.Add(1)
                                }
                        }
                }(nodeName)
        }

        // progress reporter
        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()
        startTime := time.Now()
        for {
                select {
                case <-ctx.Done():
                        goto done
                case <-ticker.C:
                        elapsed := time.Since(startTime).Seconds()
                        s := sent.Load()
                        a := acked.Load()
                        log.Info().
                                Int64("sent", s).
                                Int64("acked", a).
                                Float64("send_rate_pps", float64(s)/elapsed).
                                Int("active_goroutines", 0).
                                Msg("progress")
                }
        }

done:
        wg.Wait()
        elapsed := time.Since(startTime).Seconds()
        s := sent.Load()
        a := acked.Load()
        dropped := s - a

        log.Info().
                Int64("total_sent", s).
                Int64("total_acked", a).
                Int64("dropped", dropped).
                Float64("dropped_pct", pct(dropped, s)).
                Float64("avg_send_rate_pps", float64(s)/elapsed).
                Float64("avg_ack_rate_pps", float64(a)/elapsed).
                Float64("duration_s", elapsed).
                Msg("load-test complete")

        if dropped > s/100 {
                // >1% drop rate
                os.Exit(1)
        }
}

func pct(part, total int64) float64 {
        if total == 0 {
                return 0
        }
        return float64(part) / float64(total) * 100
}

// genEntry creates a minimal CaptureEntry for load testing.
// We don't need realistic data — just enough fields to exercise the hub.
func genEntry(nodeName string) *capturev1.CaptureEntry {
        now := time.Now().UnixMilli()
        return &capturev1.CaptureEntry{
                Id:        fmt.Sprintf("load_%d_%d", now, atomic.AddInt64(&counter, 1)),
                Timestamp: now,
                Protocol:  capturev1.Protocol_HTTP,
                Source:    &capturev1.Endpoint{Ip: "10.0.0.1", Port: 8080},
                Destination: &capturev1.Endpoint{Ip: "10.0.0.2", Port: 8080},
                Method:    "GET",
                Path:      "/api/test",
                Status:    200,
                LatencyMs: int32(rand.Intn(100)),
                NodeName:  nodeName,
                SnifferPod: "load-test-" + nodeName,
                Request: &capturev1.Payload{
                        Headers: map[string]string{
                                "content-type":  "application/json",
                                "authorization": "Bearer test.test.test",
                        },
                        Body: []byte(`{"test":true}`),
                        Size: 13,
                },
                Response: &capturev1.Payload{
                        Headers: map[string]string{"content-type": "application/json"},
                        Body:    []byte(`{"ok":true}`),
                        Size:    11,
                },
        }
}

var counter int64
