# Bullseye Production Deployment Guide

This guide walks you through deploying Bullseye to a production Kubernetes
cluster with TLS, OIDC auth, and Prometheus monitoring.

## Prerequisites

- Kubernetes 1.27+ (any distribution: EKS/GKE/AKS/kind/self-hosted)
- `kubectl` configured with cluster-admin
- [Helm 3](https://helm.sh/) (optional but recommended)
- [cert-manager](https://cert-manager.io/) installed (for TLS certs)
- An OIDC provider (Keycloak, Auth0, Okta, Dex, Google Workspace, etc.)
- A container registry (Docker Hub, ECR, GCR, private)

## Quick Start (5 minutes — dev cluster, no auth)

For a quick test on a kind/k3d cluster:

```bash
# 1. Build Docker images
./backend/scripts/build.sh --docker

# 2. Push to your registry (or load into kind)
kind load docker-image bullseye/hub:latest --name my-cluster
kind load docker-image bullseye/sniffer:latest --name my-cluster

# 3. Deploy
kubectl apply -k backend/deploy/

# 4. Port-forward to access the dashboard
kubectl -n bullseye-system port-forward svc/bullseye-hub 3030:3030

# 5. Open http://localhost:3030 in your browser
#    Set NEXT_PUBLIC_BACKEND=hub in the Next.js dashboard .env
```

⚠️ **This is dev mode — no TLS, no auth. DO NOT expose to the internet.**

## Production Deployment (30 minutes)

### Step 1: Build and push images

```bash
# Build with libpcap support (required for real capture)
docker build -t your-registry/bullseye-hub:v0.1.0 -f backend/hub/Dockerfile backend/hub/
docker build -t your-registry/bullseye-sniffer:v0.1.0 -f backend/sniffer/Dockerfile backend/sniffer/

# Push
docker push your-registry/bullseye-hub:v0.1.0
docker push your-registry/bullseye-sniffer:v0.1.0
```

### Step 2: Configure TLS with cert-manager

Create a Certificate resource. The hub will auto-detect the certs at
`/etc/bullseye/tls/`.

```yaml
# tls-certificate.yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: bullseye-hub-tls
  namespace: bullseye-system
spec:
  secretName: bullseye-hub-tls
  duration: 2160h  # 90 days
  renewBefore: 360h  # 15 days
  dnsNames:
    - bullseye-hub.bullseye-system.svc.cluster.local
    - bullseye.your-domain.com  # your ingress hostname
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
---
apiVersion: v1
kind: Secret
metadata:
  name: bullseye-hub-tls
  namespace: bullseye-system
type: kubernetes.io/tls
```

### Step 3: Configure OIDC

Create a secret with your OIDC client credentials:

```bash
kubectl -n bullseye-system create secret generic bullseye-oidc \
  --from-literal=client-secret=YOUR_OIDC_CLIENT_SECRET
```

Configure your OIDC provider (e.g., Keycloak) with:
- **Client ID**: `bullseye`
- **Redirect URI**: `https://bullseye.your-domain.com/auth/callback`
- **Scopes**: `openid profile email groups`
- **Group claim**: include groups so RBAC can use them

### Step 4: Update hub Deployment with TLS + OIDC

Edit `backend/deploy/10-hub.yaml`:

```yaml
spec:
  template:
    spec:
      containers:
        - name: hub
          env:
            # ... existing env ...
            - name: TLS_CERT_FILE
              value: /etc/bullseye/tls/tls.crt
            - name: TLS_KEY_FILE
              value: /etc/bullseye/tls/tls.key
            - name: OIDC_ISSUER
              value: https://keycloak.your-domain.com/realms/main
            - name: OIDC_CLIENT_ID
              value: bullseye
            - name: OIDC_CLIENT_SECRET
              valueFrom:
                secretKeyRef:
                  name: bullseye-oidc
                  key: client-secret
          volumeMounts:
            - name: tls
              mountPath: /etc/bullseye/tls
              readOnly: true
      volumes:
        - name: tls
          secret:
            secretName: bullseye-hub-tls
```

### Step 5: Configure Ingress

```yaml
# ingress.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: bullseye-hub
  namespace: bullseye-system
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/backend-protocol: HTTPS
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
spec:
  ingressClassName: nginx
  tls:
    - hosts: [bullseye.your-domain.com]
      secretName: bullseye-hub-tls
  rules:
    - host: bullseye.your-domain.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: bullseye-hub
                port:
                  number: 3030
```

### Step 6: Configure RBAC (namespace-scoped access)

By default, the hub's RBAC authorizer uses K8s SubjectAccessReview to check
whether a user can `list pods` in a namespace. If they can, they can see
that namespace's traffic.

To grant a user access to specific namespaces:

```bash
# Grant 'alice@example.com' read access to the 'payments' namespace
kubectl create rolebinding alice-payments-viewer \
  --clusterrole=view \
  --user=alice@example.com \
  --namespace=payments
```

To grant cluster-wide read access (admins):

```bash
kubectl create clusterrolebinding alice-cluster-viewer \
  --clusterrole=view \
  --user=alice@example.com
```

### Step 7: Configure Prometheus scraping

The hub exposes `/metrics` on port 49100. Add a ServiceMonitor (if using
the Prometheus Operator) or a scrape config:

```yaml
# servicemonitor.yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: bullseye-hub
  namespace: bullseye-system
spec:
  selector:
    matchLabels:
      app.kubernetes.io/component: hub
  endpoints:
    - port: metrics
      path: /metrics
      interval: 30s
```

Or via Helm values if using `kube-prometheus-stack`:

```yaml
prometheus:
  prometheusSpec:
    additionalScrapeConfigs: |
      - job_name: 'bullseye-hub'
        kubernetes_sd_configs:
          - role: endpoints
            namespaces:
              names: [bullseye-system]
        relabel_configs:
          - source_labels: [__meta_kubernetes_endpoint_port_name]
            action: keep
            regex: ^metrics$
```

### Step 8: Deploy

```bash
kubectl apply -f tls-certificate.yaml
kubectl apply -f ingress.yaml
kubectl apply -f servicemonitor.yaml
kubectl apply -k backend/deploy/
```

### Step 9: Verify

```bash
# Check pods are running
kubectl -n bullseye-system get pods

# Check hub logs (should show TLS enabled + auth enabled)
kubectl -n bullseye-system logs deploy/bullseye-hub | head -20

# Check metrics endpoint
kubectl -n bullseye-system port-forward svc/bullseye-hub 49100:49100
curl http://localhost:49100/metrics | grep bullseye

# Open dashboard
open https://bullseye.your-domain.com
```

## Configuration Reference

All config is via env vars (12-factor). Key ones for production:

| Env | Required | Default | Description |
|-----|----------|---------|-------------|
| `IN_CLUSTER` | yes | true | Use K8s ServiceAccount |
| `TLS_CERT_FILE` | **prod** | (empty) | Path to TLS cert (enables HTTPS) |
| `TLS_KEY_FILE` | **prod** | (empty) | Path to TLS key |
| `TLS_CA_FILE` | optional | (empty) | CA cert for mTLS (verifies sniffer client certs) |
| `OIDC_ISSUER` | **prod** | (empty) | OIDC issuer URL (enables auth when set) |
| `OIDC_CLIENT_ID` | **prod** | (empty) | OIDC client ID |
| `OIDC_CLIENT_SECRET` | **prod** | (empty) | OIDC client secret |
| `REDACT_ENABLED` | yes | true | Enable PII redaction |
| `REDACT_HEADERS` | yes | (builtins) | Comma-separated header denylist |
| `REDACT_BODY_PATTERNS` | optional | (builtins) | Comma-separated regex patterns |
| `RETENTION_TTL` | yes | 24h | Delete entries older than this |
| `STORAGE_PATH` | yes | /var/lib/bullseye/hub.db | BoltDB file path |
| `LOG_LEVEL` | yes | info | trace/debug/info/warn/error |

## Resource Sizing

### Small cluster (1-10 nodes, <1k pps)

```yaml
resources:
  requests: { cpu: 200m, memory: 512Mi }
  limits:   { cpu: 1,    memory: 2Gi }
storage: 50Gi
```

### Medium cluster (10-50 nodes, 1k-10k pps)

```yaml
resources:
  requests: { cpu: 1,    memory: 2Gi }
  limits:   { cpu: 4,    memory: 8Gi }
storage: 200Gi
```

### Large cluster (50+ nodes, >10k pps)

Switch to ClickHouse backend (planned v2). BoltDB won't keep up.

```yaml
resources:
  requests: { cpu: 4,    memory: 8Gi }
  limits:   { cpu: 16,   memory: 32Gi }
storage: 1Ti SSD
```

## Security Checklist

Before exposing to production:

- [ ] TLS enabled (`TLS_CERT_FILE` + `TLS_KEY_FILE` set)
- [ ] OIDC auth enabled (`OIDC_ISSUER` set)
- [ ] RBAC roles assigned (users can only see their namespaces)
- [ ] PII redaction enabled (default — verify `REDACT_ENABLED=true`)
- [ ] Custom redaction rules for your org (e.g., internal API keys)
- [ ] NetworkPolicy restricting access to hub (only ingress controller + sniffers)
- [ ] Secret rotation plan for OIDC client secret
- [ ] Audit logging enabled (planned v1.1 — currently no audit trail)
- [ ] Backup strategy for BoltDB PVC
- [ ] Monitoring alerts on `bullseye_hub_entries_dropped_total` (should be 0)
- [ ] Monitoring alerts on `bullseye_hub_grpc_streams_active` (should be ≥1 per node)

## Troubleshooting

### Hub won't start: "in-cluster config failed"

The hub can't find the K8s ServiceAccount token. Check:
- ServiceAccount `bullseye` exists in `bullseye-system` namespace
- Pod's `serviceAccountName: bullseye` is set
- ClusterRoleBinding grants the SA the `bullseye-reader` role

### Sniffer can't capture: "operation not permitted"

The sniffer needs `CAP_NET_RAW` and `CAP_NET_ADMIN`. Verify:
- DaemonSet's securityContext includes both capabilities
- Pod is running with `hostNetwork: true`
- Node allows privileged containers (some PSPs restrict this)

### Dashboard can't connect: WebSocket error

- Verify ingress allows WebSocket upgrades
- Check TLS cert includes the ingress hostname
- Verify OIDC redirect URI matches exactly
- Browser console should show the specific error

### High drop rate (`bullseye_hub_entries_dropped_total > 0`)

- Increase hub CPU/memory limits
- Increase `IN_MEMORY_BUFFER` (default 5000)
- Check if BoltDB write throughput is the bottleneck (switch to ClickHouse for >10k pps)
- Run `load-test` binary to find the hub's max throughput

## Backup and Recovery

BoltDB is a single file. Back it up with:

```bash
kubectl -n bullseye-system exec deploy/bullseye-hub -- \
  cp /var/lib/bullseye/hub.db /tmp/hub-backup.db

kubectl -n bullseye-system cp \
  deploy/bullseye-hub:/tmp/hub-backup.db ./hub-backup-$(date +%Y%m%d).db
```

Restore by copying the backup file back to the PVC and restarting the hub.

For automated backups, use Velero or a CronJob that runs the above.

## Upgrading

1. Build new image with new tag (e.g., `v0.2.0`)
2. Update `backend/deploy/kustomization.yaml` with new tag
3. `kubectl apply -k backend/deploy/`
4. Hub uses `strategy: Recreate` (BoltDB is single-writer) — brief downtime expected
5. Sniffer DaemonSet rolls one node at a time

## Uninstall

```bash
kubectl delete -k backend/deploy/
kubectl delete namespace bullseye-system
kubectl delete clusterrole bullseye-reader
kubectl delete clusterrolebinding bullseye-reader
```

This removes all resources. The BoltDB PVC is deleted with the namespace
unless you set a retention policy.

## Support

- **Issues**: https://github.com/your-org/bullseye/issues
- **Docs**: https://bullseye.your-domain.com/docs
- **Slack**: #bullseye on your-org.slack.com
