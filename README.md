# Bullseye — Kubernetes Network Observability

Bullseye is a **Kubernetes-first network observability and service-communication investigation tool**. It captures selected traffic visible on Kubernetes nodes, turns supported packets into request/response metadata, enriches IP addresses with Kubernetes pod, service, and namespace information, and presents the observed communication in a live dashboard.

The product is designed to help platform and application teams answer questions such as:

- Which services are communicating with each other?
- Which DNS queries are failing?
- What traffic was observed around the time an issue started?
- If a service becomes unhealthy, which observed upstream callers may be affected?
- Are repeated requests suggesting a retry storm?

Bullseye is **not currently a general-purpose cloud network monitoring platform**. It does not yet collect VPC flow logs, NetFlow, IPFIX, sFlow, switch/router telemetry, or provide complete visibility into arbitrary data-centre networks. The current deployment and enrichment model is Kubernetes-centric. A standalone Linux capture mode is possible, but it will not have Kubernetes workload names unless an enrichment source is added.

> **Implementation status:** this is an evolving project. The dashboard contains both a simulator mode and a Go-hub mode. Use the Go hub mode and verify the source of every event before treating dashboard values as live telemetry. Features described as planned or not implemented below must not be interpreted as working capabilities.

## What Bullseye does today

| Area | Current implementation |
|---|---|
| Deployment model | Kubernetes manifests: one sniffer DaemonSet pod per node, one central hub Deployment, and a dashboard |
| Packet capture | Linux AF_PACKET/libpcap capture path; requires a compatible libpcap build and packet-capture capabilities |
| Packet processing | IPv4/IPv6 TCP and UDP packet decoding through gopacket |
| DNS | Parses DNS responses and exposes query, type, response code, answers, and TTL |
| HTTP | HTTP/1 request/response parsing for supported cleartext traffic |
| TCP reconstruction | Bounded, sequence-aware TCP stream reassembly; handles in-order/out-of-order segments, retransmissions, SYN sequence space, and stream cleanup |
| Kubernetes enrichment | Maps observed endpoint IPs to pods, services, namespaces, and nodes using Kubernetes informers |
| Privacy | Hub-side header/body redaction before persistence and dashboard fan-out |
| Transport | Sniffer-to-hub bidirectional gRPC stream; current sniffer client uses insecure gRPC credentials, so production mTLS is not yet complete |
| Storage | Embedded BoltDB on the hub with a configurable TTL sweep |
| Dashboard feed | Raw WebSocket events from the hub; the frontend also supports a simulator backend |
| Hub metrics | Prometheus metrics endpoint for hub ingestion, persistence, redaction, WebSocket, and uptime metrics |

### Important protocol limitations

The packet capture filter is not the same thing as protocol support. The current sniffer capture filter is:

```
tcp or udp port 80 or port 443 or port 53 or port 8080 or port 9090 or port 6379 or port 5432 or port 9092
```

The packet processor currently has active DNS and HTTP processing paths. The TCP reassembler feeds the HTTP/1 parser. Other ports in the capture filter do **not** mean that their application protocols are decoded.

- **HTTPS/TLS:** encrypted application data remains opaque. Seeing TCP/443 does not reveal HTTP paths, headers, or bodies. The TLS decryptor package is currently a no-op integration boundary; it does not connect to a tracer or decrypt TLS.
- **HTTP/2 and gRPC:** not yet dissected by the current packet-processing path.
- **Kafka, Redis, PostgreSQL, AMQP, MySQL, MongoDB:** protocol identifiers and/or design references exist, but working dissectors are not currently registered in the active sniffer pipeline.
- **eBPF capture backend:** currently a stub, not a functioning packet collector.
- **Network devices and cloud flow logs:** NetFlow/IPFIX/sFlow and provider flow-log ingestion are not implemented.

The optional TLS tracer is planned as a separate GPL-3.0 component/repository. It is not included in this Apache-2.0 repository and is not required for ordinary DNS/HTTP capture. See [Licensing](docs/licensing.md).

## Architecture

### High-level data flow

```text
                 Kubernetes cluster
  ┌─────────────────────────────────────────────────────┐
  │                                                     │
  │  Node 1                  Node 2              Node N │
  │  ┌────────────────┐      ┌────────────────┐   ┌────┐│
  │  │ Bullseye        │      │ Bullseye        │   │ ...││
  │  │ Sniffer         │      │ Sniffer         │   │    ││
  │  │ DaemonSet pod   │      │ DaemonSet pod   │   │    ││
  │  │                 │      │                 │   │    ││
  │  │ hostNetwork     │      │ hostNetwork     │   │    ││
  │  │ AF_PACKET/pcap  │      │ AF_PACKET/pcap  │   │    ││
  │  │ BPF filter      │      │ BPF filter      │   │    ││
  │  │ TCP reassembly  │      │ TCP reassembly  │   │    ││
  │  │ DNS + HTTP/1    │      │ DNS + HTTP/1    │   │    ││
  │  └────────┬────────┘      └────────┬────────┘   └──┬─┘│
  │           └──────────────┬────────┴───────────────┘  │
  └──────────────────────────┼──────────────────────────┘
                             │ gRPC stream :9090
                             ▼
                 ┌───────────────────────────┐
                 │       Bullseye Hub        │
                 │                           │
                 │  1. Receive entries       │
                 │  2. IP → Pod/Service map  │
                 │  3. Redact sensitive data │
                 │  4. Persist to BoltDB     │
                 │  5. Publish live events   │
                 │                           │
                 │  gRPC :9090               │
                 │  WebSocket :3030          │
                 │  Prometheus :49100        │
                 └─────────────┬─────────────┘
                               │ WebSocket
                               ▼
                 ┌───────────────────────────┐
                 │     Next.js Dashboard     │
                 │                           │
                 │ Live Feed · DNS           │
                 │ Service Map · Blast Radius│
                 │ Retry Storms · Time Machine│
                 └───────────────────────────┘
```

### 1. Sniffer — capture on each Kubernetes node

The sniffer is deployed as a DaemonSet, so Kubernetes schedules one sniffer pod on each eligible node.

The manifest uses `hostNetwork: true` so the collector can observe traffic available in the node's network namespace. The container runs with the packet-capture capabilities `NET_RAW` and `NET_ADMIN`. These are privileged network operations and should be reviewed against the cluster's security policy.

The capture path is:

1. Open the configured Linux network interface (the manifest uses `any`).
2. Apply the configured Berkeley Packet Filter (BPF) expression to limit packets entering userspace.
3. Decode packets with gopacket.
4. Inspect DNS packets and TCP payloads.
5. Pass TCP payload segments through the bounded TCP assembler.
6. Feed newly contiguous HTTP/1 bytes into the HTTP parser.
7. Emit supported capture entries to the hub.

**Why TCP reassembly matters:** one HTTP request is not guaranteed to arrive in one packet. TCP can split a request across segments, reorder segments, or retransmit bytes. The assembler uses TCP sequence numbers to reconstruct contiguous stream bytes before parsing. It ignores retransmitted bytes, rejects ambiguous overlaps, expires idle state, and enforces flow and memory limits. If capture starts mid-connection, bytes that arrived before the first observed segment cannot be reconstructed reliably.

The sniffer uses a bounded output channel. If processing cannot keep up and the queue fills, entries are dropped and a warning is logged rather than allowing unbounded memory growth.

### 2. Hub — enrich, redact, store, and publish

The hub is a central Go service.

**Kubernetes enrichment**

When running in-cluster, the hub uses Kubernetes shared informers to maintain in-memory indexes of pod and Service IP addresses. For each captured endpoint, it attempts to resolve the IP to a pod or Service and adds available context such as:

- pod name
- namespace
- Kubernetes Service name
- node name

This is why Bullseye can show workload-level communication instead of only IP:port pairs. In standalone mode, Kubernetes enrichment is disabled and endpoints remain primarily network addresses.

**Redaction**

The hub applies configured header-denylist and body-regex rules, including built-in patterns for common secrets and tokens. Redaction occurs before the entry is persisted and before it is sent to the dashboard. Redaction is a safety layer, not a guarantee that every sensitive format is recognized; review capture scope and retention settings for your environment.

**Persistence**

Captured entries are stored in BoltDB, with time and service indexes and a TTL sweeper. The current Kubernetes hub manifest uses a single replica because BoltDB is a single-writer embedded database. Its example volume is `emptyDir`, which is ephemeral: data is lost when the pod is replaced. Configure durable storage and retention deliberately before relying on historical records.

**Publishing and metrics**

The hub:
- accepts sniffer streams over gRPC on port `9090`;
- publishes enriched, redacted events to connected dashboards over WebSocket on port `3030`;
- exposes hub operational metrics on port `49100`.

The current WebSocket path is a live fan-out path. Do not assume that a newly connected browser automatically receives a complete historical backlog.

### 3. Dashboard — interpret the observed traffic

The dashboard has two data-source modes:

- `simulator` (default unless overridden): connects to the TypeScript traffic simulator. This is demo data.
- `hub`: connects to the Go hub WebSocket. Set `NEXT_PUBLIC_BACKEND=hub` at frontend build time and configure `NEXT_PUBLIC_HUB_WS_URL` for the browser-reachable hub WebSocket URL.

The UI keeps a bounded in-memory traffic buffer (currently up to 1,500 entries). Dashboard analytics are computed from the entries available in that browser buffer unless a view explicitly says otherwise. A populated dashboard in simulator mode is not evidence that packet capture is working.

## Dashboard features

### Live Feed

Displays incoming traffic entries with source and destination information, protocol, request metadata, status, and latency where those fields are available. Selecting an entry opens its detail panel.

The live feed reflects events successfully captured, parsed, delivered to the hub, and forwarded to the connected browser. Missing traffic can result from capture placement, BPF filters, unsupported protocols, encryption, packet loss, queue drops, or missing Kubernetes IP enrichment.

### DNS

Shows parsed DNS response information, including the queried name, record type, response code (for example, `NOERROR` or `NXDOMAIN`), answer records, and TTL. The current packet processor emits DNS response entries; it does not provide a complete DNS resolver analytics platform.

### Service Map

Shows service communication relationships and traffic-derived statistics such as call counts, errors, and latency percentiles from the available entries. In Kubernetes mode, pod and Service metadata helps label the endpoints.

Treat an edge as an **observed communication relationship**, not proof that one service is logically or causally dependent on another. Visibility is limited to traffic observed by the deployed sniffers and protocols the collector can parse.

### Blast Radius

Blast Radius estimates which upstream callers may be affected if a selected service fails.

How the current implementation works:

1. It reads the traffic entries currently held by the dashboard.
2. It builds a directed communication graph from observed source-service → destination-service entries.
3. For the selected failing service, it walks the graph in reverse (who called this service?).
4. A breadth-first search finds direct callers and transitive callers.
5. It displays the discovered paths and traffic/error counters calculated from the available entries.

Example:

```text
checkout ──calls──▶ payment ──calls──▶ fraud-check
                         ▲
                         │ selected failing service
```

If `payment` is selected as failing, the tool looks for services that were observed calling `payment`, then callers of those services, and so on.

**Interpretation:** this is a traffic-graph impact estimate, not a fault-propagation simulator or a causal root-cause engine. It can miss dependencies when traffic was not captured, entries have aged out of the browser buffer, endpoint names are unresolved, or communication uses an unsupported/encrypted protocol. Error rates shown are derived from observed entries and should not be treated as service health SLOs.

### Retry Storms

The current UI helper groups HTTP/gRPC entries by request ID when available; otherwise it groups by destination service, method, and path. It looks for at least three matching entries inside a five-second window and assigns a simple severity based on attempt count.

This is a heuristic signal, not definitive retry detection. Without a stable request ID, legitimate repeated user requests can look like retries. The current logic should be improved with explicit retry headers, trace/span IDs, response timing, and client-side retry semantics before using it for automated incident decisions.

### Time Machine

Time Machine lets an operator replay the traffic already present in the dashboard's current in-memory buffer.

How it currently works:

1. When the Time Machine tab is opened, the UI takes a snapshot of the entries currently in memory (if a snapshot does not already exist).
2. It sorts the snapshot into chronological order.
3. A histogram summarizes entry volume and the errors recognized by the UI over that snapshot window.
4. The slider moves a playhead through the snapshot; play/pause and playback speed animate the replay.
5. The visible list shows entries up to the selected point in time.
6. **Re-snapshot** takes a new snapshot from the current browser buffer.

**Current limitation:** this is a browser-side snapshot/replay feature, not yet a durable historical query engine. It does not currently let the operator select an arbitrary date range from BoltDB, replay days of stored traffic, or guarantee a complete incident history. Its available time window is limited by the dashboard's in-memory buffer (up to 1,500 entries) and how long the browser has been receiving events. A future server-side replay API can use the hub's persistent time index for durable time-range investigations.

## Ports and deployment requirements

| Port | Purpose | Notes |
|---|---|---|
| `9090/TCP` | Sniffer → hub gRPC | Internal collector transport |
| `3030/TCP` | Hub → dashboard WebSocket | Must be reachable from the user's browser |
| `49100/TCP` | Hub Prometheus metrics | Scrape from monitoring system |
| `49101/TCP` | Sniffer metrics port declared in manifest | A working sniffer metrics HTTP handler is not currently wired in the packet path |

### Kubernetes permissions and host access

The supplied manifests create a `bullseye-system` namespace and ServiceAccount. The ClusterRole reads pods, Services, nodes, and namespaces for enrichment, and can create SubjectAccessReviews for the RBAC integration.

The sniffer DaemonSet uses host networking and packet-capture capabilities. Review:
- whether all nodes should run a sniffer;
- node selectors and tolerations;
- the exact interfaces visible to `any`;
- required Linux kernel/libpcap support;
- resource limits and packet volume;
- cluster RBAC and security-policy exceptions.

## Quick start (development / kind)

Prerequisites: Go 1.23+, Docker, `kubectl`, and `kind`.

```bash
# Build backend binaries
./backend/scripts/build.sh

# Build container images
./backend/scripts/build.sh --docker

# Create/use a kind cluster and deploy the manifests
./backend/scripts/dev-kind.sh
# Or apply the manifests to an existing test cluster:
kubectl apply -k backend/deploy/

# Forward the dashboard and hub ports (namespace: bullseye-system)
kubectl -n bullseye-system port-forward svc/bullseye-dashboard 3000:80
kubectl -n bullseye-system port-forward svc/bullseye-hub 3030:3030
```

Open the dashboard at `http://localhost:3000`. The deployment manifests are a development starting point, not a secure production deployment.

For real packet capture, the sniffer image must be built with libpcap support (the repository Dockerfile/build configuration is the source of truth). Capture requires suitable privileges and traffic must traverse an interface visible to the sniffer. If capture initialization fails, the real sniffer exits rather than silently substituting synthetic events.

### Local development

```bash
# Frontend
bun install
bun run dev

# Go backend tests
cd backend/sniffer
go test ./...

# Build backend images/binaries
cd ../..
./backend/scripts/build.sh
```

The frontend defaults to simulator mode. To exercise the Go hub path, build/configure the frontend with:

```bash
NEXT_PUBLIC_BACKEND=hub
NEXT_PUBLIC_HUB_WS_URL=ws://localhost:3030/
```

Because `NEXT_PUBLIC_*` values are embedded into the frontend at build time, set them before building the dashboard image. Use `wss://` behind a correctly configured HTTPS endpoint.

## Security and production-readiness notes

The included manifests are intentionally a starting point. Before production use:

- Configure authenticated, encrypted sniffer-to-hub transport. The current sniffer gRPC client uses insecure credentials; mTLS is not complete.
- Configure and verify OIDC authentication and enforce authorization on every API/WebSocket path. Do not assume that the presence of RBAC helper code means every request is protected.
- Restrict WebSocket origins; do not expose an unrestricted WebSocket endpoint.
- Replace the example `emptyDir` with a durable volume and validate backups, restore, retention, and disk capacity.
- Keep the hub at one replica while using the current BoltDB design.
- Run the collector with the minimum capabilities and node access necessary.
- Redact sensitive headers and payload patterns; minimize captured data and restrict who can inspect it.
- Validate packet loss, queue saturation, restart behaviour, and hub outage behaviour under load.
- Do not enable TLS decryption or privileged eBPF tracing without explicit security, privacy, and licensing review.
- Treat Blast Radius, Retry Storms, and all derived analytics as investigative signals, not automatic proof of causality.
- Keep response actions read-only until explicit approval gates and rollback safeguards exist.

## Repository layout

- `src/` — Next.js dashboard and client-side analytics.
- `src/store/traffic-store.ts` — live event buffer, backend selection, and Time Machine snapshot state.
- `src/lib/stats.ts` — service statistics, Blast Radius graph traversal, and Retry Storm heuristics.
- `backend/sniffer/` — Linux packet capture, TCP reassembly, DNS/HTTP processing, and gRPC client.
- `backend/hub/` — gRPC ingestion, Kubernetes enrichment, redaction, BoltDB persistence, WebSocket publishing, and Prometheus metrics.
- `backend/proto/` — protobuf contract between sniffer and hub.
- `backend/deploy/` — Kubernetes namespace, RBAC, hub, sniffer DaemonSet, and dashboard manifests.
- `docs/` — architecture and licensing documentation.

## Licensing

Bullseye core is licensed under Apache-2.0. The sniffer, hub, protobuf contract, dashboard, and Kubernetes manifests in this repository remain within the core license boundary.

The optional TLS tracer is not included here. It is planned as a separately distributed GPL-3.0 component/repository, subject to upstream licensing and attribution review. The current `backend/sniffer/internal/tls` package is only a no-op boundary; it does not decrypt TLS.

See [docs/licensing.md](docs/licensing.md).

## Roadmap

The next engineering milestones should prioritize correctness and trustworthiness:

1. Complete secure sniffer-to-hub transport (mTLS, identity, certificate rotation) and authenticated/authorized dashboard access.
2. Add collector health, packet-drop visibility, reconnect/replay semantics, and reliable bounded buffering.
3. Improve HTTP/1 request/response pairing and protocol detection; add tested HTTP/2 and gRPC dissection.
4. Add durable server-side time-range query/replay APIs backed by the hub store.
5. Improve dependency graph provenance, Blast Radius confidence, and retry detection using trace/request correlation.
6. Add supported Kubernetes metadata and OpenTelemetry correlation with explicit source and confidence.
7. Evaluate a scalable storage backend and multi-replica hub only after load tests demonstrate the need.
8. Consider additional environments (VMs, bare metal, cloud flow logs, network devices) as separate, validated collection integrations—not assumed capabilities.

## Contributing

Please open an issue or pull request for feature proposals and bug reports. Include reproducible steps, test coverage, supported kernel/runtime details, and whether data is real or synthetic.

A passing build or unit test suite is not, by itself, evidence of production readiness. Validate capture behaviour on a controlled Linux/Kubernetes test environment before relying on Bullseye for incident response.
