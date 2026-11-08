// Package main — fake-sniffer is a development tool that streams synthetic
// CaptureEntry messages to the Bullseye hub via gRPC.
//
// It replays the same traffic patterns as the TypeScript traffic-simulator
// (mini-services/traffic-simulator), but over the real sniffer→hub gRPC
// contract instead of socket.io. This lets you test the full hub pipeline
// (enrichment → redaction → persistence → UI relay) without needing a real
// K8s cluster or eBPF sniffer.
//
// Usage:
//   go run ./cmd/fake-sniffer --hub=localhost:9090
package main

import (
        "context"
        "flag"
        "fmt"
        "math/rand"
        "os"
        "os/signal"
        "strings"
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
        rate := flag.Float64("rate", 8.0, "entries per second")
        nodeName := flag.String("node", "fake-node-1", "K8s node name to impersonate")
        flag.Parse()

        zerolog.SetGlobalLevel(zerolog.InfoLevel)
        log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

        log.Info().
                Str("hub", *hubAddr).
                Float64("rate", *rate).
                Str("node", *nodeName).
                Msg("bullseye fake-sniffer starting")

        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        sigCh := make(chan os.Signal, 1)
        signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
        go func() {
                <-sigCh
                log.Info().Msg("shutting down")
                cancel()
        }()

        // Connect to hub
        conn, err := grpc.NewClient(*hubAddr,
                grpc.WithTransportCredentials(insecure.NewCredentials()),
                grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(16*1024*1024)),
        )
        if err != nil {
                log.Fatal().Err(err).Msg("dial hub failed")
        }
        defer conn.Close()

        client := capturev1.NewCaptureServiceClient(conn)
        stream, err := client.StreamEntries(ctx)
        if err != nil {
                log.Fatal().Err(err).Msg("open stream failed")
        }

        // reader goroutine — logs acks
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

        // stream entries at the configured rate
        ticker := time.NewTicker(time.Duration(float64(time.Second) / *rate))
        defer ticker.Stop()

        var sent atomic.Int64
        stormActive := false
        stormUntil := time.Now()

        for {
                select {
                case <-ctx.Done():
                        log.Info().Int64("sent", sent.Load()).Msg("stopping")
                        _ = stream.CloseSend()
                        return
                case <-ticker.C:
                        // occasionally trigger a retry storm (every ~30s, lasts 10s)
                        now := time.Now()
                        if !stormActive && now.After(stormUntil) && rand.Float64() < 0.05 {
                                stormActive = true
                                stormUntil = now.Add(10 * time.Second)
                                log.Info().Msg("retry storm starting")
                        }
                        if stormActive && now.After(stormUntil) {
                                stormActive = false
                        }

                        entry := genEntry(now.UnixMilli(), *nodeName, stormActive)
                        if err := stream.Send(entry); err != nil {
                                log.Warn().Err(err).Msg("send failed")
                                return
                        }
                        sent.Add(1)
                }
        }
}

// --- entry generation (mirrors the TS simulator) ---

var counter uint64

type svc struct {
        name, ns, ip string
        port         int32
        proto        capturev1.Protocol
}

var services = []svc{
        {"ingress-nginx-controller", "ingress-nginx", "10.0.1.12", 443, capturev1.Protocol_HTTP},
        {"checkout-api", "checkout", "10.0.2.21", 8080, capturev1.Protocol_HTTP},
        {"payments-api", "payments", "10.0.3.45", 8080, capturev1.Protocol_HTTP},
        {"payments-worker", "payments", "10.0.3.47", 9090, capturev1.Protocol_GRPC},
        {"catalog-api", "catalog", "10.0.4.11", 8080, capturev1.Protocol_HTTP},
        {"users-api", "users", "10.0.5.30", 8080, capturev1.Protocol_HTTP},
        {"shipping-svc", "shipping", "10.0.6.18", 8080, capturev1.Protocol_HTTP},
        {"cart-svc", "checkout", "10.0.2.55", 8080, capturev1.Protocol_HTTP},
        {"rating-svc", "catalog", "10.0.4.40", 8080, capturev1.Protocol_HTTP},
        {"redis-cart", "checkout", "10.0.2.99", 6379, capturev1.Protocol_REDIS},
        {"postgres-payments", "payments", "10.0.3.200", 5432, capturev1.Protocol_POSTGRES},
        {"kafka-broker", "analytics", "10.0.7.50", 9092, capturev1.Protocol_KAFKA},
        {"kube-dns", "kube-system", "10.0.0.10", 53, capturev1.Protocol_DNS},
        {"external-stripe", "external", "3.121.10.5", 443, capturev1.Protocol_HTTP},
}

type edge struct {
        src, dst int
        weight   int
        proto    capturev1.Protocol
        paths    []pathSpec
        grpcM    string
        redisCmd []string
        kafkaT   string
}

type pathSpec struct {
        method string
        path   string
        status []int32
}

var edges = []edge{
        {0, 1, 25, capturev1.Protocol_HTTP, []pathSpec{
                {"GET", "/api/checkout/cart", []int32{200, 200, 200, 401, 500}},
                {"POST", "/api/checkout/order", []int32{200, 200, 201, 422, 503}},
                {"GET", "/api/checkout/orders/{id}", []int32{200, 200, 404}},
        }, "", nil, ""},
        {0, 2, 30, capturev1.Protocol_HTTP, []pathSpec{
                {"GET", "/api/catalog/products", []int32{200, 200, 200, 503}},
                {"GET", "/api/catalog/products/{id}", []int32{200, 200, 404}},
                {"GET", "/api/catalog/search?q=shoe", []int32{200, 200, 500}},
        }, "", nil, ""},
        {0, 3, 18, capturev1.Protocol_HTTP, []pathSpec{
                {"POST", "/api/users/login", []int32{200, 200, 401, 500}},
                {"GET", "/api/users/me", []int32{200, 200, 401}},
        }, "", nil, ""},
        {1, 2, 14, capturev1.Protocol_HTTP, []pathSpec{
                {"POST", "/v1/charges", []int32{200, 200, 201, 402, 500, 503}},
                {"GET", "/v1/charges/{id}", []int32{200, 404}},
                {"POST", "/v1/refunds", []int32{200, 422}},
        }, "", nil, ""},
        {1, 7, 12, capturev1.Protocol_HTTP, []pathSpec{
                {"GET", "/cart/{user_id}", []int32{200, 200, 404}},
                {"POST", "/cart/{user_id}/items", []int32{200, 201, 422}},
        }, "", nil, ""},
        {2, 8, 10, capturev1.Protocol_HTTP, []pathSpec{
                {"GET", "/ratings/{product_id}", []int32{200, 200, 500}},
                {"POST", "/ratings/{product_id}", []int32{200, 201, 429}},
        }, "", nil, ""},
        {2, 10, 6, capturev1.Protocol_HTTP, []pathSpec{
                {"POST", "/v1/charges", []int32{200, 200, 402, 502}},
                {"GET", "/v1/charges/{id}", []int32{200, 404}},
        }, "", nil, ""},
        {7, 9, 20, capturev1.Protocol_REDIS, nil, "", []string{"GET cart:{user}", "SET cart:{user}", "HGETALL cart:{user}", "EXPIRE cart:{user} 900"}, ""},
        {3, 11, 5, capturev1.Protocol_KAFKA, nil, "", nil, "user-events"},
        {2, 4, 7, capturev1.Protocol_GRPC, nil, "", nil, ""}, // grpcM set in init()
        {1, 12, 9, capturev1.Protocol_DNS, nil, "", nil, ""},
        {2, 12, 8, capturev1.Protocol_DNS, nil, "", nil, ""},
        {3, 12, 7, capturev1.Protocol_DNS, nil, "", nil, ""},
}

func init() {
        edges[9].grpcM = "payments.v1.Worker/ProcessRefund"
}

var totalWeight = func() int {
        t := 0
        for _, e := range edges {
                t += e.weight
        }
        return t
}()

func pickEdge() *edge {
        r := rand.Float64() * float64(totalWeight)
        for i := range edges {
                r -= float64(edges[i].weight)
                if r <= 0 {
                        return &edges[i]
                }
        }
        return &edges[len(edges)-1]
}

func genEntry(ts int64, node string, storm bool) *capturev1.CaptureEntry {
        var e *edge
        forceStatus := int32(0)
        reqID := ""
        isRetry := false
        if storm && rand.Float64() < 0.7 {
                // retry storm — POST /v1/charges with 5xx
                e = &edges[3]
                reqID = fmt.Sprintf("req_storm_%d", ts/1000)
                forceStatus = []int32{503, 503, 502, 500, 504}[rand.Intn(5)]
                isRetry = true
        } else {
                e = pickEdge()
        }

        src := services[e.src]
        dst := services[e.dst]

        entry := &capturev1.CaptureEntry{
                Id:        fmt.Sprintf("pkt_%d_%d", ts, atomic.AddUint64(&counter, 1)),
                Timestamp: ts,
                Protocol:  e.proto,
                Tls:       dst.ns == "external" || (src.ns == "ingress-nginx" && dst.port == 443),
                Source: &capturev1.Endpoint{
                        Ip:   src.ip,
                        Port: src.port,
                },
                Destination: &capturev1.Endpoint{
                        Ip:   dst.ip,
                        Port: dst.port,
                },
                RequestId: reqID,
                IsRetry:   isRetry,
                NodeName:  node,
                SnifferPod: fmt.Sprintf("bullseye-sniffer-%s", node),
        }

        // NOTE: source/destination pod names are NOT set here. The hub's enricher
        // fills them in via K8s informer lookups. For local testing (no K8s),
        // the enricher will return nil and the entries will show as just IPs.

        switch e.proto {
        case capturev1.Protocol_HTTP:
                ps := e.paths[rand.Intn(len(e.paths))]
                method := ps.method
                path := fillTemplate(ps.path)
                status := forceStatus
                if status == 0 {
                        status = ps.status[rand.Intn(len(ps.status))]
                }
                latency := int32(50)
                if status >= 500 {
                        latency = int32(800 + rand.Intn(2700))
                } else {
                        latency = int32(8 + rand.Intn(212))
                }
                entry.Method = method
                entry.Path = path
                entry.Status = status
                entry.LatencyMs = latency
                entry.Request = &capturev1.Payload{
                        Headers: map[string]string{
                                "content-type":  "application/json",
                                "user-agent":    fmt.Sprintf("%s/v1.4.2 (k8s)", src.name),
                                "x-request-id":  reqIDOrRandom(reqID),
                                "x-b3-traceid":  randHex(16),
                                "authorization": "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyXzg4MjEifQ.signature",
                        },
                        Body: []byte(sampleReqBody(path)),
                        Size: int32(rand.Intn(1200) + 80),
                }
                entry.Response = &capturev1.Payload{
                        Headers: map[string]string{
                                "content-type": "application/json",
                                "x-request-id": entry.Request.Headers["x-request-id"],
                                "date":         time.UnixMilli(ts).UTC().Format(time.RFC1123),
                        },
                        Body: []byte(sampleRespBody(status, path)),
                        Size: int32(rand.Intn(2400) + 120),
                }

        case capturev1.Protocol_GRPC:
                status := forceStatus
                if status == 0 {
                        status = []int32{0, 0, 0, 13, 14}[rand.Intn(5)]
                }
                entry.Method = e.grpcM
                entry.Status = status
                entry.LatencyMs = int32(rand.Intn(1200))
                entry.Request = &capturev1.Payload{
                        Headers: map[string]string{"content-type": "application/grpc"},
                        Body:    []byte(`{"refund_id":"ord_550e8400","amount_cents":4999}`),
                        Size:    80,
                }
                entry.Response = &capturev1.Payload{
                        Headers: map[string]string{"grpc-status": fmt.Sprintf("%d", status)},
                        Body:    []byte(`{"ok":true}`),
                        Size:    40,
                }

        case capturev1.Protocol_REDIS:
                cmd := e.redisCmd[rand.Intn(len(e.redisCmd))]
                cmd = strings.ReplaceAll(cmd, "{user}", fmt.Sprintf("u_%d", 1000+rand.Intn(9000)))
                entry.Method = strings.SplitN(cmd, " ", 2)[0]
                entry.Path = strings.SplitN(cmd, " ", 2)[1]
                entry.Status = 200
                entry.LatencyMs = int32(1 + rand.Intn(12))
                entry.Request = &capturev1.Payload{
                        Headers: map[string]string{},
                        Body:    []byte(cmd),
                        Size:    int32(len(cmd)),
                }
                entry.Response = &capturev1.Payload{
                        Headers: map[string]string{},
                        Body:    []byte("OK"),
                        Size:    4,
                }

        case capturev1.Protocol_KAFKA:
                entry.Method = "PRODUCE"
                entry.Path = "topic=" + e.kafkaT
                entry.Status = 200
                entry.LatencyMs = int32(3 + rand.Intn(40))
                entry.Request = &capturev1.Payload{
                        Headers: map[string]string{"correlation-id": fmt.Sprintf("k_%d", 1000+rand.Intn(9000))},
                        Body:    []byte(fmt.Sprintf(`{"topic":"%s","partition":%d,"event":"user.login"}`, e.kafkaT, rand.Intn(4))),
                        Size:    100,
                }
                entry.Response = &capturev1.Payload{
                        Headers: map[string]string{},
                        Body:    []byte(`{"offset":12345}`),
                        Size:    30,
                }

        case capturev1.Protocol_DNS:
                queries := []string{
                        "payments-api.payments.svc.cluster.local",
                        "checkout-api.checkout.svc.cluster.local",
                        "catalog-api.catalog.svc.cluster.local",
                        "redis-cart.checkout.svc.cluster.local",
                }
                q := queries[rand.Intn(len(queries))]
                nx := rand.Float64() < 0.08
                entry.DnsQuery = q
                entry.DnsType = "A"
                if nx {
                        entry.DnsRcode = "NXDOMAIN"
                        entry.DnsAnswer = []string{}
                        entry.DnsTtl = 0
                        entry.Status = 3
                } else {
                        entry.DnsRcode = "NOERROR"
                        entry.DnsAnswer = []string{fmt.Sprintf("10.0.%d.%d", 2+rand.Intn(6), 2+rand.Intn(250))}
                        entry.DnsTtl = int32(5 + rand.Intn(25))
                        entry.Status = 0
                }
                entry.LatencyMs = int32(1 + rand.Intn(8))
                entry.Method = "QUERY"
                entry.Path = q
                entry.Request = &capturev1.Payload{
                        Headers: map[string]string{},
                        Body:    []byte(fmt.Sprintf("QUERY %s A", q)),
                        Size:    int32(len(q) + 8),
                }
                entry.Response = &capturev1.Payload{
                        Headers: map[string]string{},
                        Body:    []byte(q + " A " + entry.DnsRcode),
                        Size:    50,
                }

        case capturev1.Protocol_POSTGRES:
                entry.Method = "SELECT"
                entry.Path = "payments WHERE id=$1"
                entry.Status = 200
                entry.LatencyMs = int32(20 + rand.Intn(180))
                entry.Request = &capturev1.Payload{
                        Headers: map[string]string{},
                        Body:    []byte("SELECT * FROM payments WHERE id = $1"),
                        Size:    40,
                }
                entry.Response = &capturev1.Payload{
                        Headers: map[string]string{},
                        Body:    []byte(`{"id":"ord_550e8400","amount":4999}`),
                        Size:    50,
                }
        }

        return entry
}

func fillTemplate(s string) string {
        r := strings.NewReplacer(
                "{id}", []string{"ord_550e8400", "ord_6ba0b4ff", "p_sku_001", "p_sku_042"}[rand.Intn(4)],
                "{user_id}", fmt.Sprintf("u_%d", 1000+rand.Intn(9000)),
                "{product_id}", []string{"p_sku_001", "p_sku_042", "p_sku_118"}[rand.Intn(3)],
        )
        return r.Replace(s)
}

func reqIDOrRandom(r string) string {
        if r != "" {
                return r
        }
        return fmt.Sprintf("req_%s", randHex(8))
}

func randHex(n int) string {
        const hex = "0123456789abcdef"
        b := make([]byte, n)
        for i := range b {
                b[i] = hex[rand.Intn(16)]
        }
        return string(b)
}

func sampleReqBody(path string) string {
        if strings.Contains(path, "/login") {
                return `{"email":"alice@shop.io","password":"hunter2"}`
        }
        if strings.Contains(path, "/order") {
                return `{"user_id":"u_8821","items":[{"sku":"p_sku_042","qty":2}],"total_cents":12999}`
        }
        if strings.Contains(path, "/charges") {
                return `{"amount":4999,"currency":"usd","source":"tok_visa"}`
        }
        return `{"ok":true}`
}

func sampleRespBody(status int32, path string) string {
        if status >= 500 {
                return fmt.Sprintf(`{"error":"internal_server_error","request_id":"req_%s"}`, randHex(8))
        }
        if status == 401 {
                return `{"error":"unauthorized","message":"token expired"}`
        }
        if status == 404 {
                return `{"error":"not_found"}`
        }
        if strings.Contains(path, "/login") {
                return `{"token":"eyJhbGciOiJIUzI1NiJ9","user_id":"u_8821","expires_in":3600}`
        }
        if strings.Contains(path, "/charges") {
                return fmt.Sprintf(`{"id":"ch_%s","amount":4999,"status":"succeeded"}`, randHex(10))
        }
        if strings.Contains(path, "/products") {
                return `{"id":"p_sku_042","name":"Wireless Headphones","price_cents":12999}`
        }
        return `{"ok":true}`
}
