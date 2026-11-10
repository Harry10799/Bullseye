# Bullseye licensing and component boundary

## Core

The Bullseye repository is intended to be distributed under Apache-2.0. The root `LICENSE` contains the license text. Core components in this repository include the sniffer, hub, protobuf definitions, dashboard, and their build/deployment assets.

Do not copy GPL-licensed tracer source, generated GPL code, or GPL-only implementation dependencies into these core components. A protocol client that communicates with a separately distributed process is the intended integration boundary; the protocol client itself must be independently authored and Apache-2.0 compatible.

## Optional TLS tracer

The optional `bullseye-tracer` is planned for v0.2 in a separate repository and distribution under GPL-3.0, after reviewing the exact upstream source, notices, attribution, and applicable license obligations. No tracer source is vendored in this repository today.

The tracer is not required for packet capture, DNS, plaintext protocol parsing, or normal sniffer startup. If it is not installed or unavailable, TLS remains opaque and the collector continues. Packet capture on TCP/443 does not decrypt TLS.

The intended integration is a versioned gRPC API. A loopback address such as `127.0.0.1:9091` is valid when both containers share a Kubernetes Pod network namespace. If deployed across hosts or network namespaces, use an explicitly configured endpoint and authenticated transport. The protocol must define connection tuple identity, stream IDs, ordering, timestamps, bounded message sizes, backpressure, and data-retention rules.

## Security requirements for future plaintext tracing

- Require explicit opt-in; never enable decryption merely because the tracer image is present.
- Restrict access to the tracer API to the sniffer and protect it with peer authentication appropriate to the deployment.
- Apply capture allowlists and redact secrets/credentials before persistence or dashboard delivery.
- Bound buffers, message sizes, connection counts, and retention; drop safely under pressure.
- Expose tracer health and dropped-event counters without making tracer availability a collector liveness dependency.
- Document kernel, capability, privilege, and supported TLS-library requirements.
- Publish the GPL tracer as a separate image and source repository with its own LICENSE, notices, SBOM, and source-offer/compliance process as applicable.

## CI enforcement

The license workflow checks that core source does not contain GPL SPDX identifiers or references to a vendored tracer implementation, and checks Go module dependency license metadata for disallowed GPL-family licenses. This is an automated guardrail, not a substitute for a human review of new dependencies, copied code, generated sources, or distribution artifacts.
