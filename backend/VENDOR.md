# Vendoring Kubeshark's Sniffer Code

This document describes how to extract the AF_PACKET capture path and L7
dissectors from Kubeshark (Apache-2.0) into Bullseye's sniffer.

**You only need to do this once.** After vendoring, the Bullseye sniffer
binary contains real capture + dissection logic and can run on any K8s node.

## Licensing

| Component | Source Repo | License | Vendored Into |
|-----------|-------------|---------|---------------|
| AF_PACKET sniffer | `kubeshark/kubeshark` | Apache-2.0 | `backend/sniffer/internal/capture/afpacket/` |
| L7 dissectors (HTTP, gRPC, DNS, Kafka, Redis, AMQP, MySQL, MongoDB) | `kubeshark/kubeshark` | Apache-2.0 | `backend/sniffer/internal/dissect/` |
| TCP reassembly | `kubeshark/kubeshark` | Apache-2.0 | `backend/sniffer/internal/reassembly/` |
| **eBPF TLS tracer** | `kubeshark/tracer` | **GPL-3.0** | **separate sidecar** (NOT vendored into Apache binary) |

The license boundary is:
- `backend/sniffer/` — Apache-2.0 only. Contains NO GPL code.
- `backend/tracer/` — GPL-3.0. Built as a separate container image. Communicates
  with the sniffer via localhost gRPC. This keeps the sniffer's license clean.

## Step 1: Clone Kubeshark

```bash
git clone https://github.com/kubeshark/kubeshark.git /tmp/kubeshark
cd /tmp/kubeshark
git checkout v1.x  # pin to a release tag
```

## Step 2: Locate the sniffer code

Kubeshark's repo structure (as of v1.x):

```
kubeshark/
├── cmd/                  # CLI entry points (kubeshark CLI tool)
├── internal/
│   └── connect/          # hub connection logic
├── kubernetes/           # K8s manifest templates
├── misc/                 # shared utilities
├── helm-chart/           # Helm chart sources
└── ...
```

The actual sniffer + dissector code lives in **a separate worker binary**
that's built from the same repo but lives in a sub-module. To find it:

```bash
# search for the AF_PACKET capture code
grep -rn "AF_PACKET" --include="*.go" /tmp/kubeshark/

# search for HTTP dissector
grep -rn "func.*[Dd]issect.*HTTP" --include="*.go" /tmp/kubeshark/

# find the worker entry point
find /tmp/kubeshark -name "main.go" | xargs grep -l "sniffer\|worker"
```

> **Note**: Kubeshark's repo structure may differ from what's documented
> above — they've reorganized over time. If the paths below don't match,
> use the grep commands above to locate the actual files.

## Step 3: Vendor the AF_PACKET capture

```bash
# From the bullseye repo root:
mkdir -p backend/sniffer/internal/capture/afpacket

# Copy the AF_PACKET implementation. Adjust paths based on Step 2 findings.
cp /tmp/kubeshark/<path-to-afpacket>/*.go \
   backend/sniffer/internal/capture/afpacket/

# Add Apache-2.0 license header to each file (preserves attribution)
for f in backend/sniffer/internal/capture/afpacket/*.go; do
  sed -i '1i // SPDX-License-Identifier: Apache-2.0\n// SPDX-FileCopyrightText: Copyright Kubeshark contributors\n//\n// This file is derived from Kubeshark (https://github.com/kubeshark/kubeshark)\n// and is used under the Apache License, Version 2.0.\n' "$f"
done
```

Update `backend/sniffer/internal/capture/capturer.go` — replace the stub
`NewAFPacket` with the real implementation:

```go
func NewAFPacket(cfg Config) (Capturer, error) {
    return afpacket.New(afpacket.Config{
        Interface: cfg.Interface,
        Promisc:   cfg.Promisc,
        Filter:    cfg.BPFFilter,
    })
}
```

## Step 4: Vendor the L7 dissectors

```bash
mkdir -p backend/sniffer/internal/dissect/{http,grpc,dns,kafka,redis,amqp,mysql,mongodb}

for proto in http grpc dns kafka redis amqp mysql mongodb; do
  cp /tmp/kubeshark/<path-to-$proto-dissector>/*.go \
     backend/sniffer/internal/dissect/$proto/
done

# Add license headers (same as Step 3)
```

Wire up each dissector in `backend/sniffer/internal/dissect/dissect.go`:

```go
func NewRegistry() *Registry {
    r := &Registry{byPort: make(map[int]Dissector)}
    r.Register(http.New())     // ports 80, 8080, 8000, 3000
    r.Register(grpc.New())     // port 9090 + http2
    r.Register(dns.New())      // port 53
    r.Register(kafka.New())    // port 9092
    r.Register(redis.New())    // port 6379
    r.Register(amqp.New())     // port 5672
    r.Register(mysql.New())    // port 3306
    r.Register(mongodb.New())  // port 27017
    return r
}
```

## Step 5: Vendor TCP reassembly

Kubeshark uses `google/gopacket` for TCP reassembly. Vendor their wrapper:

```bash
mkdir -p backend/sniffer/internal/reassembly
cp /tmp/kubeshark/<path-to-reassembly>/*.go \
   backend/sniffer/internal/reassembly/
```

## Step 6: (Optional) Vendor the eBPF TLS tracer — GPL-3.0 sidecar

The TLS tracer is in a separate repo: `kubeshark/tracer` (GPL-3.0).

```bash
git clone https://github.com/kubeshark/tracer.git /tmp/kubeshark-tracer

mkdir -p backend/tracer
cp -r /tmp/kubeshark-tracer/* backend/tracer/

# Add GPL-3.0 header
cat > backend/tracer/LICENSE <<'EOF'
SPDX-License-Identifier: GPL-3.0-or-later

This package is derived from kubeshark/tracer
(https://github.com/kubeshark/tracer) and is licensed under GPL-3.0.
EOF
```

Build the tracer as a **separate** container image:

```dockerfile
# backend/tracer/Dockerfile
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y clang llvm libbpf-dev
COPY . /src
WORKDIR /src
RUN make
ENTRYPOINT ["/src/tracer"]
```

The tracer is deployed as a sidecar in the sniffer pod (see
`backend/deploy/20-sniffer-daemonset.yaml` — uncomment the tracer container).
The Apache-licensed sniffer binary contains NO GPL code; it communicates with
the GPL tracer via localhost gRPC on port 9091.

## Step 7: Verify license boundary

After vendoring, run:

```bash
# Ensure no GPL code leaked into the Apache sniffer binary
grep -r "GPL" backend/sniffer/ && echo "FAIL: GPL found in sniffer" || echo "OK: sniffer is Apache-clean"

# Ensure Apache files have SPDX headers
grep -rL "SPDX-License-Identifier: Apache-2.0" backend/sniffer/internal/ && \
  echo "WARN: some files missing SPDX headers" || echo "OK: all SPDX headers present"
```

## Step 8: Build and test

```bash
./backend/scripts/build.sh
# or with docker:
./backend/scripts/build.sh --docker

# deploy to local kind cluster:
./backend/scripts/dev-kind.sh
```

## Maintenance

- **Upstream updates**: re-run the extraction steps when Kubeshark releases
  new dissectors or fixes capture bugs. Pin to a release tag for stability.
- **License audits**: run the Step 7 checks in CI.
- **Modifications**: when you modify vendored code, mark your changes clearly
  with `// BULLSEYE:` comments so future re-vendoring doesn't lose them.

## Alternative: write your own dissectors

If you want to avoid the licensing complexity entirely, you can:

1. Use `google/gopacket` directly (Apache-2.0) for TCP reassembly
2. Write dissectors from scratch using protocol RFCs
3. Use `cilium/ebpf` (Apache-2.0) for eBPF capture (no TLS decryption)

This is ~2-3 months of work for one engineer but gives you full ownership
of the code with no attribution requirements.

## References

- Kubeshark main repo: https://github.com/kubeshark/kubeshark (Apache-2.0)
- Kubeshark tracer: https://github.com/kubeshark/tracer (GPL-3.0)
- Kubeshark anatomy docs: https://docs.kubeshark.com/en/anatomy_of_kubeshark
- Kubeshark RBAC docs: https://docs.kubeshark.com/en/permissions
- Apache-2.0 license: https://www.apache.org/licenses/LICENSE-2.0
- GPL-3.0 license: https://www.gnu.org/licenses/gpl-3.0.html
