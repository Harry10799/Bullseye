# Bullseye — Intelligent Network Observability

Bullseye is an intelligent network observability platform for traditional and hybrid infrastructure: Linux and Windows servers, virtual machines, network devices, data centres, and cloud networks.

Its primary focus is **network intelligence**: understanding traffic behaviour, discovering communication relationships, detecting abnormal network conditions, and helping operators investigate incidents with evidence.

> **Project status:** this repository contains an evolving implementation. Do not treat dashboard data as production telemetry unless it is explicitly marked as live and sourced from a functioning collector.

## Product direction

- Network-wide visibility across hosts, services, and network segments.
- Traffic and protocol metadata collection with privacy-aware filtering.
- Service and network dependency discovery.
- DNS, latency, throughput, retransmission, reset, and packet-drop insights where supported.
- Behaviour baselines, anomaly detection, and evidence-backed incident investigation.
- A clear distinction between live telemetry and synthetic/demo data.

Kubernetes metadata is optional enrichment; Bullseye does not require Kubernetes.

## High-level architecture

1. **Collectors** run on selected Linux hosts and observation points. Host telemetry can use eBPF; selective packet metadata capture can use AF_PACKET/libpcap. Network devices can contribute NetFlow, IPFIX, or sFlow records.
2. **Local processing** applies capture filters, protocol parsing, aggregation, redaction, and bounded buffering.
3. **Secure transport** sends telemetry to the central hub over authenticated encrypted connections (mTLS is the production target).
4. **Central processing** validates and enriches events, aggregates flows, maintains topology relationships, evaluates alerts, and exposes query APIs.
5. **Storage** is selected by data type and measured scale: Prometheus-compatible metrics storage, a columnar store such as ClickHouse for flow history, and PostgreSQL for configuration and identity.
6. **Dashboard** presents network overview, topology, traffic analytics, anomaly investigation, and collector health.

See [docs/architecture/network-intelligence.md](docs/architecture/network-intelligence.md) for the detailed product direction.

## Repository areas

- `src/` — Next.js dashboard.
- `backend/sniffer/` — Go collector and packet processing.
- `backend/hub/` — central ingestion and API services.
- `backend/proto/` — protobuf definitions.
- `docs/` — architecture, security, and operational documentation.

## TLS visibility and licensing boundary

**Bullseye core is licensed under Apache-2.0.** The core sniffer, hub, protocol definitions, and dashboard do not include GPL tracer source or a GPL tracer dependency.

TLS decryption is **not included in the default deployment**. The optional `bullseye-tracer` sidecar is planned for v0.2 as a separately distributed component/repository under GPL-3.0, subject to upstream license and attribution review. It is not vendored into this repository and is not required for Bullseye operation.

Without the optional tracer, Bullseye can still collect supported plaintext protocols and DNS, and can report available TLS connection metadata (for example, handshake metadata) where implemented. Capturing port 443 does not itself decrypt HTTPS. Do not assume HTTP paths, headers, or bodies are visible inside encrypted TLS sessions.

The future tracer integration will use a documented gRPC boundary. A localhost endpoint is appropriate when both containers share a Pod network namespace; deployments outside that model must use an explicitly configured, authenticated endpoint. Tracer unavailability must not stop ordinary packet capture.

See [docs/licensing.md](docs/licensing.md) for the component boundary and release policy.

## Running and development

Follow the package manifests and service-specific instructions in the repository.

- Keep local environment configuration in an ignored `.env` file.
- Use `.env.example` only for placeholders; never put real credentials in it.
- Run frontend and Go backend tests/builds independently.
- Use explicit synthetic/demo mode for UI and pipeline testing.
- Validate packet-capture privileges, interface selection, kernel support, and libpcap build tags on the target Linux host.

### Sniffer modes

Synthetic mode is explicitly enabled with `-rate <entries-per-second>` and is intended only for testing. Without that flag, failure to initialize real packet capture is an error; Bullseye must not silently present synthetic events as live network traffic.

## Security and operational cautions

- Do not commit `.env`, credentials, private keys, or customer packet payloads.
- Use TLS/mTLS and authenticated authorization for production service-to-service and user-facing traffic.
- Restrict WebSocket origins and enforce authentication/authorization at the API boundary.
- Apply data minimization and redaction before persistence or forwarding.
- Treat AI-generated explanations as hypotheses backed by telemetry, not authoritative root-cause conclusions.
- Start with read-only investigation. Do not automate firewall, routing, or other disruptive changes without explicit approvals and safeguards.

## Roadmap

1. Reliable real telemetry collection, explicit demo mode, secure transport, collector health, and reconnect/buffering behaviour.
2. Flow aggregation, metrics, and searchable historical data.
3. Network topology discovery, baselines, anomaly detection, and event correlation.
4. Evidence-based AI incident investigation with read-only tools.
5. Optional TLS tracer sidecar (separate GPL-3.0 distribution), multi-site operations, high availability, packaging, and scale validation.

A successful build is not proof of production readiness. Before deployment, run tests, security review, packet-capture validation, load tests, and failure/recovery tests in a controlled environment.
