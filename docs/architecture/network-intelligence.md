# Network Intelligence Architecture

## Goal

Make Bullseye a network-first observability platform for traditional and hybrid infrastructure. Kubernetes is an optional metadata source, not a prerequisite.

## Observation sources

- Linux hosts: eBPF host/network telemetry where supported; selective AF_PACKET/libpcap capture for packet-derived metadata.
- Network devices: NetFlow, IPFIX, and sFlow from routers, switches, and supported appliances.
- Windows hosts and appliances: supported OS/network exporters or vendor telemetry APIs. Do not assume Linux packet-capture agents run everywhere.
- Cloud and virtual networks: provider flow logs and network telemetry, identified as distinct sources.

Start with a documented source matrix covering kernel, privilege, interface, protocol, and version requirements.

## Collector responsibilities

1. Identify the host and observation point with stable source metadata.
2. Capture only traffic and metadata required by the configured use case.
3. Parse supported protocols defensively; do not guess at malformed or encrypted traffic.
4. Redact or omit sensitive headers and payload data before forwarding.
5. Aggregate high-volume observations where possible; use bounded queues and backpressure.
6. Expose collector health, capture errors, dropped events, queue depth, and last successful send time.
7. Forward events over authenticated encrypted transport; production deployments should use mTLS and certificate rotation.

Synthetic data must be opt-in and visibly labelled as demo data. Failure to initialize real capture must be surfaced as an error, never silently replaced with generated traffic.

## Central intelligence pipeline

1. **Ingestion:** authenticate collector identity, validate schema/version, apply quotas, and reject malformed events.
2. **Normalization:** standardize timestamps, endpoint identity, protocol names, direction, and observation-point metadata.
3. **Enrichment:** add inventory, subnet/site, DNS, cloud, and optional Kubernetes metadata with provenance.
4. **Flow processing:** aggregate by time window and stable flow keys; preserve source and sampling information.
5. **Topology:** infer observed communication edges and label them as observed relationships, not proof of causal dependency.
6. **Detection:** calculate network baselines and evaluate latency, retransmits, resets, DNS, drops, throughput, and traffic-volume deviations where measurements exist.
7. **Investigation:** correlate alerts and telemetry into a timeline with evidence and suggested confirmation steps.

## Storage boundaries

- Metrics: Prometheus-compatible time-series storage for rates, gauges, counters, and alert evaluation.
- Flow history: a columnar analytics database such as ClickHouse when volume and query patterns justify it.
- Configuration, users, and inventory: PostgreSQL or an equivalent transactional database.
- Collector spool: bounded local disk or embedded queue for short outages, with retention limits and replay semantics.

Do not introduce Kafka/NATS or multiple databases by default. Add components only when measured throughput, durability, or operational needs justify them.

## AI investigation guardrails

- Expose read-only, structured query tools over normalized telemetry and inventory.
- Require each finding to cite the time range, entities, and observed measurements.
- Phrase conclusions as likely causes unless evidence proves otherwise.
- Include alternative explanations and concrete confirmation tests.
- Do not permit automatic firewall, routing, ACL, or host changes in initial releases.

## Security baseline

- mTLS for collector-to-hub communication in production.
- Authentication and authorization on HTTP and WebSocket endpoints.
- Explicit WebSocket origin allowlists; no wildcard origin policy for authenticated production sessions.
- Secret management outside source control; redact credentials and sensitive payload fields.
- Least-privilege capture capabilities and documented packet visibility limitations.
- Retention, access audit, and customer/site isolation before multi-tenant production use.

## Delivery phases and acceptance scenarios

### Phase 1 — trustworthy collection

- Real capture succeeds on a documented Linux test host.
- Capture initialization failure is visible and does not emit synthetic events.
- Synthetic mode works only when explicitly requested and is labelled synthetic.
- Collector-to-hub delivery errors and collector health are observable.
- Two hosts report traffic with distinct source identities.

### Phase 2 — network telemetry

- Flow records are normalized and queryable over a time range.
- Metrics expose throughput, event rates, capture drops, and collector health.
- Restart and temporary hub outage behaviour is tested; queue limits and data-loss semantics are documented.

### Phase 3 — network intelligence

- Topology edges show observation provenance and confidence.
- Baselines account for time windows and seasonality where data volume allows.
- Alerts include thresholds, affected entities, and evidence links.
- Detection quality is evaluated against labelled test scenarios.

### Phase 4 — assisted investigation

- An incident timeline combines alerts, flows, DNS, and host metrics.
- AI summaries cite telemetry evidence and state uncertainty.
- Suggested tests are read-only and reproducible.

### Phase 5 — production operations

- Multi-site isolation, HA, backups, upgrade/rollback, capacity planning, and load testing are documented.
- Security review covers authentication, authorization, origin policy, secrets, retention, and packet-data exposure.
