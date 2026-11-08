# Bullseye Backend

Go backend for Bullseye — the Wireshark-for-Kubernetes tool with unique
features Kubeshark doesn't have (Time Machine, Blast Radius, Retry Storms,
Deploy Diff, OTel correlation, Anomaly detection).

## Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                    Bullseye Cluster                              │
│                                                                  │
│  ┌────────────────┐  ┌────────────────┐  ┌────────────────┐      │
│  │  sniffer       │  │  sniffer       │  │  sniffer       │      │
│  │  (DaemonSet)   │  │  (DaemonSet)   │  │  (DaemonSet)   │      │
│  │  node-1        │  │  node-2        │  │  node-N        │      │
│  │                │  │                │  │                │      │
│  │ ┌────────────┐ │  │  ┌──────────┐  │  │ ┌────────────┐ │      │
│  │ │ AF_PACKET  │ │  │  │ AF_PACKET│  │  │ │ AF_PACKET  │ │      │
│  │ │ (Apache)   │ │  │  │ (Apache) │  │  │ │ (Apache)   │ │      │
│  │ └────────────┘ │  │  └──────────┘  │  │ └────────────┘ │      │
│  │ ┌────────────┐ │  │                │  │ ┌────────────┐ │      │
│  │ │ TLS tracer │ │  │  (optional)    │  │ │ TLS tracer │ │      │
│  │ │ (GPL-3.0)  │ │  │                │  │ │ (GPL-3.0)  │ │      │
│  │ │ sidecar    │ │  │                │  │ │ sidecar    │ │      │
│  │ └────────────┘ │  │                │  │ └────────────┘ │      │
│  └───────┬────────┘  └───────┬────────┘  └───────┬────────┘      │
│          │ gRPC 9090         │                   │                │
│          └───────────────────┼───────────────────┘                │
│                              ▼                                    │
│                   ┌────────────────────┐                          │
│                   │   Hub (Go)         │  K8s informers           │
│                   │  - enrich (IP→pod) │  PII redaction           │
│                   │  - redact          │  RBAC (SAR)              │
│                   │  - rbac            │  BoltDB persistence      │
│                   │  - store (BoltDB)  │  PCAP export             │
│                   │  - pcap            │                          │
│                   └─────────┬──────────┘                          │
│                             │                                     │
│              ┌──────────────┼──────────────┐                     │
│              ▼              ▼              ▼                      │
│      ┌──────────────┐ ┌───────────┐ ┌──────────────┐             │
│      │ WebSocket    │ │ Metrics   │ │ gRPC         │             │
│      │ :3030 (UI)   │ │ :49100    │ │ :9090 (snif) │             │
│      └──────┬───────┘ └───────────┘ └──────────────┘             │
│             │                                                    │
│             ▼                                                    │
│      ┌──────────────────────┐                                   │
│      │  Next.js Dashboard   │  ← /home/z/my-project/src/         │
│      │  (existing UI)       │     Time Machine                   │
│      │                      │     Blast Radius                   │
│      │                      │     Retry Storms                   │
│      └──────────────────────┘                                   │
└──────────────────────────────────────────────────────────────────┘
```

## Licensing Strategy

| Component | License | Location |
|-----------|---------|----------|
| Hub (this dir) | Apache-2.0 | `backend/hub/` |
| Sniffer core (AF_PACKET + dissectors) | Apache-2.0 | `backend/sniffer/` |
| Proto contract | Apache-2.0 | `backend/proto/` |
| K8s manifests | Apache-2.0 | `backend/deploy/` |
| **TLS tracer** (eBPF uprobes) | **GPL-3.0** | `backend/tracer/` (separate) |

The GPL TLS tracer runs as a **separate sidecar container** and communicates
with the Apache sniffer via localhost gRPC. This keeps the sniffer binary's
license clean. See `VENDOR.md` for extraction instructions.

## Quick Start

### Prereqs

- Go 1.23+
- Docker (for container builds)
- `kind` (for local K8s dev)
- `kubectl`

### Build

```bash
# build binaries
./backend/scripts/build.sh

# build docker images
./backend/scripts/build.sh --docker
```

### Generate protobuf (after editing .proto)

```bash
./backend/scripts/gen-proto.sh
```

### Local dev cluster

```bash
./backend/scripts/dev-kind.sh
# → dashboard at http://localhost:3030
# → gRPC at localhost:9090
```

### Vendor Kubeshark's sniffer code (one-time)

The sniffer currently has stub capture/dissector code. To get real capture:

```bash
# follow backend/VENDOR.md
```

## Project Structure

```
backend/
├── proto/
│   ├── bullseye.proto           # sniffer→hub gRPC contract
│   └── bullseye/v1/             # generated Go code (run gen-proto.sh)
├── hub/                         # central collector (Apache-2.0)
│   ├── cmd/hub/main.go          # entry point
│   ├── internal/
│   │   ├── config/              # env-based config
│   │   ├── enrich/              # K8s informers, IP→pod map
│   │   ├── redact/              # PII redaction (headers + body regex)
│   │   ├── rbac/                # SubjectAccessReview-based authz
│   │   ├── store/               # BoltDB persistence + TTL sweep
│   │   ├── pcap/                # real .pcap export via gopacket
│   │   └── api/                 # WebSocket (UI) + gRPC (sniffer)
│   ├── Dockerfile
│   └── go.mod
├── sniffer/                     # DaemonSet (Apache-2.0 core)
│   ├── cmd/sniffer/main.go      # entry point
│   ├── internal/
│   │   ├── capture/             # AF_PACKET + eBPF backends
│   │   ├── dissect/             # L7 protocol dissectors
│   │   ├── tls/                 # TLS decryption (via GPL sidecar)
│   │   └── emit/                # gRPC stream to hub
│   ├── Dockerfile
│   └── go.mod
├── deploy/                      # K8s manifests
│   ├── 00-rbac.yaml             # Namespace, SA, ClusterRole
│   ├── 10-hub.yaml              # Hub Deployment + Service + PVC
│   ├── 20-sniffer-daemonset.yaml# Sniffer DaemonSet (+ optional tracer)
│   └── kustomization.yaml
├── scripts/
│   ├── build.sh                 # build binaries or docker images
│   ├── dev-kind.sh              # local kind cluster + deploy
│   └── gen-proto.sh             # generate Go from .proto
├── VENDOR.md                    # how to extract Kubeshark's code
└── README.md                    # this file
```

## Configuration

All config is via env vars (12-factor). See `hub/internal/config/config.go`
for the full list. Key ones:

| Env | Default | Description |
|-----|---------|-------------|
| `GRPC_PORT` | 9090 | sniffer→hub gRPC port |
| `WS_PORT` | 3030 | hub→UI WebSocket port |
| `METRICS_PORT` | 49100 | Prometheus metrics port |
| `IN_CLUSTER` | true | use K8s ServiceAccount |
| `STORAGE_PATH` | /var/lib/bullseye/hub.db | BoltDB file path |
| `RETENTION_TTL` | 24h | delete entries older than this |
| `IN_MEMORY_BUFFER` | 5000 | ring buffer size for UI backlog |
| `REDACT_ENABLED` | true | enable PII redaction |
| `REDACT_HEADERS` | authorization,cookie,... | comma-separated denylist |
| `REDACT_BODY_PATTERNS` | (builtins) | comma-separated regex patterns |
| `OIDC_ISSUER` | (empty) | enable OIDC auth when set |
| `LOG_LEVEL` | info | trace, debug, info, warn, error |

## Roadmap to Production

See `../README.md` (project root) for the 90-day roadmap. Status:

- [x] Hub architecture (gRPC + WS + K8s informers + redact + RBAC + BoltDB + PCAP)
- [x] Sniffer scaffolding (AF_PACKET + eBPF backends, dissector registry, emit)
- [x] Proto contract
- [x] K8s manifests (RBAC, Hub Deployment, Sniffer DaemonSet)
- [x] Build + dev scripts
- [ ] **NEXT**: Vendor Kubeshark's AF_PACKET + dissectors (see VENDOR.md)
- [ ] Generate proto code (run gen-proto.sh)
- [ ] Wire gRPC server into hub main.go
- [ ] Test end-to-end on kind cluster
- [ ] Build TLS tracer sidecar (GPL-3.0, separate repo)
- [ ] OIDC auth flow
- [ ] ClickHouse storage backend (for >50 node clusters)

## Unique Features (vs Kubeshark)

Built in the dashboard UI (already working with sample data):

1. **Time Machine / Replay** — scrub through historical traffic, play/pause
2. **Blast Radius** — failure-impact analysis via reverse-caller BFS
3. **Retry Storm Detector** — clusters of repeated requests within 5s window

Planned (see roadmap):

4. Deploy Diff
5. OTel trace correlation
6. Anomaly detection
7. SLO burn-rate alerts
8. Case files / save & share
9. OpenAPI inference

## License

Apache-2.0 for all code in this `backend/` directory, EXCEPT:

- `backend/tracer/` (when added) — GPL-3.0 (eBPF TLS tracer, forked from kubeshark/tracer)

The GPL component is isolated as a separate sidecar container to keep the
core Bullseye distribution Apache-2.0.
