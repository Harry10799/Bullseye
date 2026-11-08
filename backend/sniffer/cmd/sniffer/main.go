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

	"github.com/bullseye/bullseye/backend/sniffer/internal/capture"
	"github.com/bullseye/bullseye/backend/sniffer/internal/dissect"
	"github.com/bullseye/bullseye/backend/sniffer/internal/emit"
	"github.com/bullseye/bullseye/backend/sniffer/internal/tls"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	hubAddr := flag.String("hub", "bullseye-hub:9090", "hub gRPC address")
	iface := flag.String("iface", "any", "network interface to capture on")
	backend := flag.String("backend", "afpacket", "capture backend: afpacket | ebpf")
	tlsTracer := flag.String("tls-tracer", "localhost:9091", "TLS tracer sidecar address (empty to disable)")
	nodeName := flag.String("node", "", "K8s node name (auto-detected from env)")
	flag.Parse()

	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	// auto-detect node name from env (set by K8s downward API)
	if *nodeName == "" {
		*nodeName = os.Getenv("NODE_NAME")
	}

	log.Info().
		Str("hub", *hubAddr).
		Str("iface", *iface).
		Str("backend", *backend).
		Str("node", *nodeName).
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

	// 1. Connect to hub
	emitter, err := emit.New(ctx, *hubAddr)
	if err != nil {
		log.Fatal().Err(err).Msg("emit init failed")
	}
	defer emitter.Close()
	go emitter.RunReconnect(ctx)

	// 2. TLS decryptor (optional)
	if *tlsTracer != "" {
		dec := tls.New(*tlsTracer)
		go dec.Start(ctx)
	}

	// 3. Capture
	cap, err := capture.New(capture.Config{
		Backend:   *backend,
		Interface: *iface,
		Promisc:   true,
		BPFFilter: "tcp or udp port 80 or port 443 or port 53 or port 8080 or port 9090 or port 6379 or port 5432 or port 9092",
	})
	if err != nil {
		log.Fatal().Err(err).Msg("capture init failed")
	}
	go func() {
		if err := cap.Start(ctx); err != nil {
			log.Error().Err(err).Msg("capture stopped with error")
		}
	}()

	// 4. Dissectors
	registry := dissect.NewRegistry()
	log.Info().Int("dissectors", len(registry.All())).Msg("dissectors registered")

	// 5. Main loop: capture → dissect → enrich → emit
	//    (Once Kubeshark's AF_PACKET + dissector code is vendored, this loop
	//    reads packets from cap, reassembles TCP streams, runs dissectors,
	//    pairs request/response, and emits CaptureEntry messages to hub.)
	log.Info().Msg("sniffer ready — waiting for vendored Kubeshark code (see VENDOR.md)")

	<-ctx.Done()
	fmt.Fprintln(os.Stderr, "sniffer stopped")
}
