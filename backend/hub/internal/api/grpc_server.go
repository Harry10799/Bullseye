// Package api — gRPC server that receives CaptureEntry messages from sniffers.
// This is the sniffer → hub side; the dashboard UI side is in server.go.
package api

import (
        "fmt"
        "io"
        "sync/atomic"
        "time"

        capturev1 "github.com/bullseye/bullseye/backend/proto/bullseye/v1"
        "github.com/rs/zerolog/log"
        "google.golang.org/grpc/codes"
        "google.golang.org/grpc/status"
)

// CaptureServer implements the sniffer → hub gRPC contract.
// It receives a stream of CaptureEntry messages from each sniffer pod,
// enriches them with K8s context, applies PII redaction, persists, and
// relays to dashboards.
type CaptureServer struct {
        capturev1.UnimplementedCaptureServiceServer

        Enricher Enricher
        Redactor Redactor
        Store    Store
        WS       *Server // dashboard fan-out

        // stats
        entriesReceived atomic.Int64
        entriesDropped  atomic.Int64
}

// Enricher interface — implemented by *enrich.Enricher.
type Enricher interface {
        LookupPod(ip string) *PodInfo
        LookupService(ip string) *ServiceInfo
        LookupServiceForPod(podName, namespace string) []string
}

// PodInfo / ServiceInfo are duplicated here as interfaces so the api package
// doesn't import enrich directly (avoid import cycles).
type PodInfo struct {
        Name      string
        Namespace string
        IP        string
        HostIP    string
        NodeName  string
}

type ServiceInfo struct {
        Name      string
        Namespace string
        ClusterIP string
}

// Redactor interface — implemented by *redact.Redactor.
type Redactor interface {
        RedactHeaders(headers map[string]string) int
        RedactBody(body []byte) ([]byte, bool)
}

// Store interface — implemented by *store.Store.
type Store interface {
        Put(entry *StoreEntry) error
        Recent(n int) ([]*StoreEntry, error)
}

// StoreEntry is the storage-layer entry shape (decoupled from wire format).
type StoreEntry struct {
        ID          string
        Timestamp   int64
        Protocol    string
        TLS         bool
        Source      StoreEndpoint
        Destination StoreEndpoint
        Method      string
        Path        string
        Status      int32
        LatencyMs   int32
        RequestID   string
        IsRetry     bool
        Request     StorePayload
        Response    StorePayload
        GrpcStatus  string
        DNSQuery    string
        DNSType     string
        DNSRcode    string
        DNSAnswer   []string
        DNSTtl      int32
        NodeName    string
        SnifferPod  string
}

type StoreEndpoint struct {
        IP      string
        Port    int32
        Pod     string
        Service string
        NS      string
}

type StorePayload struct {
        Headers map[string]string
        Body    []byte
        Size    int32
}

// StreamEntries is the bidirectional RPC. Each sniffer opens one stream and
// sends CaptureEntry messages; the hub responds with periodic Acks.
func (s *CaptureServer) StreamEntries(stream capturev1.CaptureService_StreamEntriesServer) error {
        ctx := stream.Context()
        nodeName := "(unknown)" // populated from first entry

        // ack ticker — send one Ack every 2 seconds or every 100 entries
        ticker := time.NewTicker(2 * time.Second)
        defer ticker.Stop()
        entryCount := 0

        for {
                select {
                case <-ctx.Done():
                        log.Info().Int("count", entryCount).Str("node", nodeName).
                                Msg("capture: sniffer stream closed")
                        return ctx.Err()
                default:
                }

                entry, err := stream.Recv()
                if err == io.EOF {
                        log.Info().Int("count", entryCount).Str("node", nodeName).
                                Msg("capture: sniffer stream ended (EOF)")
                        return nil
                }
                if err != nil {
                        return status.Errorf(codes.Aborted, "recv: %v", err)
                }

                if entry.NodeName != "" && nodeName == "(unknown)" {
                        nodeName = entry.NodeName
                        log.Info().Str("node", nodeName).Msg("capture: sniffer connected")
                }

                s.entriesReceived.Add(1)
                entryCount++

                // process + relay
                uiEntry := s.processEntry(entry)
                if uiEntry != nil && s.WS != nil {
                        s.WS.Submit(uiEntry)
                }

                // periodic ack — every 100 entries or 2 seconds
                if entryCount%100 == 0 {
                        ack := &capturev1.Ack{
                                EntriesReceived: int32(entryCount),
                                LastEntryId:     entry.Id,
                        }
                        if err := stream.Send(ack); err != nil {
                                log.Warn().Err(err).Msg("capture: send ack failed")
                        }
                }

                // also tick-based ack (covers low-traffic sniffers)
                select {
                case <-ticker.C:
                        ack := &capturev1.Ack{
                                EntriesReceived: int32(entryCount),
                                LastEntryId:     entry.Id,
                        }
                        if err := stream.Send(ack); err != nil {
                                log.Warn().Err(err).Msg("capture: send tick ack failed")
                        }
                default:
                }
        }
}

// processEntry enriches, redacts, persists, and converts to UI shape.
func (s *CaptureServer) processEntry(entry *capturev1.CaptureEntry) *UIEntry {
        if entry == nil {
                return nil
        }

        // 1. enrich with K8s metadata
        src := UIEndpoint{IP: entry.Source.Ip, Port: entry.Source.Port}
        if s.Enricher != nil {
                if pod := s.Enricher.LookupPod(entry.Source.Ip); pod != nil {
                        src.Pod = pod.Name
                        src.NS = pod.Namespace
                        if svcs := s.Enricher.LookupServiceForPod(pod.Name, pod.Namespace); len(svcs) > 0 {
                                src.Service = svcs[0]
                        }
                }
        }

        dst := UIEndpoint{IP: entry.Destination.Ip, Port: entry.Destination.Port}
        if s.Enricher != nil {
                if pod := s.Enricher.LookupPod(entry.Destination.Ip); pod != nil {
                        dst.Pod = pod.Name
                        dst.NS = pod.Namespace
                }
                if svc := s.Enricher.LookupService(entry.Destination.Ip); svc != nil {
                        dst.Service = svc.Name
                        dst.NS = svc.Namespace
                }
        }

        // 2. redact PII
        var reqHeaders, respHeaders map[string]string
        var reqBody, respBody []byte
        if entry.Request != nil {
                reqHeaders = entry.Request.Headers
                reqBody = entry.Request.Body
        }
        if entry.Response != nil {
                respHeaders = entry.Response.Headers
                respBody = entry.Response.Body
        }
        if s.Redactor != nil {
                s.Redactor.RedactHeaders(reqHeaders)
                s.Redactor.RedactHeaders(respHeaders)
                reqBody, _ = s.Redactor.RedactBody(reqBody)
                respBody, _ = s.Redactor.RedactBody(respBody)
        }

        // 3. build UI entry
        protoName := protocolName(entry.Protocol)
        ui := &UIEntry{
                ID:          entry.Id,
                Timestamp:   entry.Timestamp,
                Protocol:    protoName,
                TLS:         entry.Tls,
                Source:      src,
                Destination: dst,
                Method:      entry.Method,
                Path:        entry.Path,
                Status:      entry.Status,
                LatencyMs:   entry.LatencyMs,
                RequestID:   entry.RequestId,
                IsRetry:     entry.IsRetry,
                Request: UIPayload{
                        Headers: reqHeaders,
                        Body:    string(reqBody),
                        Size:    int32(len(reqBody)),
                },
                Response: UIPayload{
                        Headers: respHeaders,
                        Body:    string(respBody),
                        Size:    int32(len(respBody)),
                },
                GrpcStatus: entry.GrpcStatus,
                DNSQuery:   entry.DnsQuery,
                DNSType:    entry.DnsType,
                DNSRcode:   entry.DnsRcode,
                DNSAnswer:  entry.DnsAnswer,
                DNSTtl:     entry.DnsTtl,
        }

        // 4. persist (sync — BoltDB writes are fast)
        if s.Store != nil {
                storeEntry := &StoreEntry{
                        ID:          entry.Id,
                        Timestamp:   entry.Timestamp,
                        Protocol:    protoName,
                        TLS:         entry.Tls,
                        Source:      StoreEndpoint{IP: src.IP, Port: src.Port, Pod: src.Pod, Service: src.Service, NS: src.NS},
                        Destination: StoreEndpoint{IP: dst.IP, Port: dst.Port, Pod: dst.Pod, Service: dst.Service, NS: dst.NS},
                        Method:      entry.Method,
                        Path:        entry.Path,
                        Status:      entry.Status,
                        LatencyMs:   entry.LatencyMs,
                        RequestID:   entry.RequestId,
                        IsRetry:     entry.IsRetry,
                        Request:     StorePayload{Headers: reqHeaders, Body: reqBody, Size: int32(len(reqBody))},
                        Response:    StorePayload{Headers: respHeaders, Body: respBody, Size: int32(len(respBody))},
                        GrpcStatus:  entry.GrpcStatus,
                        DNSQuery:    entry.DnsQuery,
                        DNSType:     entry.DnsType,
                        DNSRcode:    entry.DnsRcode,
                        DNSAnswer:   entry.DnsAnswer,
                        DNSTtl:      entry.DnsTtl,
                        NodeName:    entry.NodeName,
                        SnifferPod:  entry.SnifferPod,
                }
                if err := s.Store.Put(storeEntry); err != nil {
                        log.Warn().Err(err).Str("entry_id", entry.Id).Msg("capture: store put failed")
                }
        }

        return ui
}

// Stats returns counters for the metrics endpoint.
func (s *CaptureServer) Stats() (received, dropped int64) {
        return s.entriesReceived.Load(), s.entriesDropped.Load()
}

// protocolName converts the protobuf Protocol enum to the UI string.
func protocolName(p capturev1.Protocol) string {
        switch p {
        case capturev1.Protocol_HTTP:
                return "HTTP"
        case capturev1.Protocol_GRPC:
                return "gRPC"
        case capturev1.Protocol_DNS:
                return "DNS"
        case capturev1.Protocol_KAFKA:
                return "Kafka"
        case capturev1.Protocol_REDIS:
                return "Redis"
        case capturev1.Protocol_POSTGRES:
                return "Postgres"
        case capturev1.Protocol_AMQP:
                return "AMQP"
        case capturev1.Protocol_MYSQL:
                return "MySQL"
        case capturev1.Protocol_MONGODB:
                return "MongoDB"
        default:
                return "UNKNOWN"
        }
}

// Suppress unused-import warning for fmt (used by future error formatting).
var _ = fmt.Sprintf
