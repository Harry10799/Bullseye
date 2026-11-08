// Package api — gRPC server that receives CaptureEntry messages from sniffers.
// This is the sniffer → hub side; the dashboard UI side is in server.go.
package api

import (
        "context"
        "fmt"
        "io"
        "sync"
        "sync/atomic"
        "time"

        "github.com/rs/zerolog/log"
        "google.golang.org/grpc/codes"
        "google.golang.org/grpc/status"
)

// CaptureServer implements the sniffer → hub gRPC contract.
// It receives a stream of CaptureEntry messages from each sniffer pod,
// enriches them with K8s context, applies PII redaction, persists, and
// relays to dashboards.
type CaptureServer struct {
        UnimplementedCaptureServiceServer

        enricher Enricher
        redactor Redactor
        store    Store
        ws       *Server // dashboard fan-out

        // stats
        entriesReceived atomic.Int64
        entriesDropped  atomic.Int64
        lastSeenByNode  sync.Map // node_name -> time.Time
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

// UnimplementedCaptureServiceServer is a placeholder until we generate the
// real protobuf types. Replace with capturev1.UnimplementedCaptureServiceServer
// after running gen-proto.sh.
type UnimplementedCaptureServiceServer struct{}

// StreamEntries is the bidirectional RPC. Each sniffer opens one stream and
// sends CaptureEntry messages; the hub responds with periodic Acks.
//
// NOTE: This signature uses interface{} for the request type because we
// haven't generated the proto code yet. After running gen-proto.sh, replace
// with the generated CaptureService_StreamEntriesServer type.
func (s *CaptureServer) StreamEntries(stream interface{}) error {
        ctx := context.Background()
        _ = ctx

        // ack ticker — send one Ack every 2 seconds or 100 entries
        ticker := time.NewTicker(2 * time.Second)
        defer ticker.Stop()
        entryCount := 0
        lastAck := time.Now()

        for {
                // read next entry — using interface{} placeholder
                // In production: entry, err := stream.Recv()
                entry, err := recvEntry(stream)
                if err == io.EOF {
                        log.Info().Int("count", entryCount).Msg("capture: sniffer stream ended")
                        return nil
                }
                if err != nil {
                        return status.Errorf(codes.Aborted, "recv: %v", err)
                }

                s.entriesReceived.Add(1)
                entryCount++

                // enrich + redact + persist + relay
                uiEntry := s.processEntry(entry)
                if uiEntry != nil {
                        s.ws.Submit(uiEntry)
                }

                // send periodic ack
                if time.Since(lastAck) > 2*time.Second || entryCount%100 == 0 {
                        _ = sendAck(stream, entryCount, false, 1)
                        lastAck = time.Now()
                }
                _ = ticker
        }
}

// processEntry enriches, redacts, persists, and converts to UI shape.
func (s *CaptureServer) processEntry(entry *RawEntry) *UIEntry {
        if entry == nil {
                return nil
        }

        // 1. enrich with K8s metadata
        srcPod := s.enricher.LookupPod(entry.SourceIP)
        dstPod := s.enricher.LookupPod(entry.DestinationIP)
        dstSvc := s.enricher.LookupService(entry.DestinationIP)

        src := UIEndpoint{
                IP:   entry.SourceIP,
                Port: entry.SourcePort,
        }
        if srcPod != nil {
                src.Pod = srcPod.Name
                src.NS = srcPod.Namespace
                // find service that selects this pod
                if svcs := s.enricher.LookupServiceForPod(srcPod.Name, srcPod.Namespace); len(svcs) > 0 {
                        src.Service = svcs[0]
                }
        }

        dst := UIEndpoint{
                IP:   entry.DestinationIP,
                Port: entry.DestinationPort,
        }
        if dstPod != nil {
                dst.Pod = dstPod.Name
                dst.NS = dstPod.Namespace
        }
        if dstSvc != nil {
                dst.Service = dstSvc.Name
                dst.NS = dstSvc.Namespace
        }

        // 2. redact PII
        reqHeaders := entry.RequestHeaders
        respHeaders := entry.ResponseHeaders
        reqBody := entry.RequestBody
        respBody := entry.ResponseBody
        if s.redactor != nil {
                s.redactor.RedactHeaders(reqHeaders)
                s.redactor.RedactHeaders(respHeaders)
                reqBody, _ = s.redactor.RedactBody(reqBody)
                respBody, _ = s.redactor.RedactBody(respBody)
        }

        // 3. build UI entry
        ui := &UIEntry{
                ID:        entry.ID,
                Timestamp: entry.Timestamp,
                Protocol:  entry.Protocol,
                TLS:       entry.TLS,
                Source:    src,
                Destination: dst,
                Method:    entry.Method,
                Path:      entry.Path,
                Status:    entry.Status,
                LatencyMs: entry.LatencyMs,
                RequestID: entry.RequestID,
                IsRetry:   entry.IsRetry,
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
                DNSQuery:   entry.DNSQuery,
                DNSType:    entry.DNSType,
                DNSRcode:   entry.DNSRcode,
                DNSAnswer:  entry.DNSAnswer,
                DNSTtl:     entry.DNSTtl,
        }

        // 4. persist (async to avoid blocking the stream)
        storeEntry := &StoreEntry{
                ID:          entry.ID,
                Timestamp:   entry.Timestamp,
                Protocol:    entry.Protocol,
                TLS:         entry.TLS,
                Source:      StoreEndpoint{IP: src.IP, Port: src.Port, Pod: src.Pod, Service: src.Service, NS: src.NS},
                Destination: StoreEndpoint{IP: dst.IP, Port: dst.Port, Pod: dst.Pod, Service: dst.Service, NS: dst.NS},
                Method:      entry.Method,
                Path:        entry.Path,
                Status:      entry.Status,
                LatencyMs:   entry.LatencyMs,
                RequestID:   entry.RequestID,
                IsRetry:     entry.IsRetry,
                Request:     StorePayload{Headers: reqHeaders, Body: reqBody, Size: int32(len(reqBody))},
                Response:    StorePayload{Headers: respHeaders, Body: respBody, Size: int32(len(respBody))},
                GrpcStatus:  entry.GrpcStatus,
                DNSQuery:    entry.DNSQuery,
                DNSType:     entry.DNSType,
                DNSRcode:    entry.DNSRcode,
                DNSAnswer:   entry.DNSAnswer,
                DNSTtl:      entry.DNSTtl,
                NodeName:    entry.NodeName,
                SnifferPod:  entry.SnifferPod,
        }
        if s.store != nil {
                if err := s.store.Put(storeEntry); err != nil {
                        log.Warn().Err(err).Str("entry_id", entry.ID).Msg("capture: store put failed")
                }
        }

        return ui
}

// Stats returns counters for the metrics endpoint.
func (s *CaptureServer) Stats() (received, dropped int64) {
        return s.entriesReceived.Load(), s.entriesDropped.Load()
}

// RawEntry is the in-memory shape of a sniffer's CaptureEntry, before
// enrichment. After running gen-proto.sh, replace with the generated type.
type RawEntry struct {
        ID            string
        Timestamp     int64
        Protocol      string
        TLS           bool
        SourceIP      string
        SourcePort    int32
        DestinationIP string
        DestinationPort int32
        Method        string
        Path          string
        Status        int32
        LatencyMs     int32
        RequestID     string
        IsRetry       bool
        RequestHeaders  map[string]string
        RequestBody    []byte
        ResponseHeaders map[string]string
        ResponseBody   []byte
        GrpcStatus    string
        DNSQuery      string
        DNSType       string
        DNSRcode      string
        DNSAnswer     []string
        DNSTtl        int32
        NodeName      string
        SnifferPod    string
}

// recvEntry / sendAck are placeholders. After running gen-proto.sh and
// replacing the stream type, these become trivial wrappers around the
// generated grpc stream methods.
func recvEntry(stream interface{}) (*RawEntry, error) {
        // TODO: replace with: entry, err := stream.(CaptureService_StreamEntriesServer).Recv()
        return nil, fmt.Errorf("not implemented — run gen-proto.sh")
}

func sendAck(stream interface{}, count int, throttle bool, sampleRate int) error {
        // TODO: replace with: stream.(CaptureService_StreamEntriesServer).Send(&Ack{...})
        return nil
}

// lastSeenByNode tracks the last time we heard from each sniffer node.
// Used for health monitoring.
var lastSeenByNode sync.Map // node_name -> time.Time
