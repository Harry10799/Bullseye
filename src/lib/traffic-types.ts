// Shared types for Kubeshark UI — mirror what the real sniffer+hub would emit.

export type Protocol = 'HTTP' | 'gRPC' | 'DNS' | 'Kafka' | 'Redis' | 'Postgres'

export interface Endpoint {
  ns: string
  pod: string
  service: string
  ip: string
  port: number
}

export interface TrafficEntry {
  id: string
  timestamp: number
  protocol: Protocol
  tls: boolean
  source: Endpoint
  destination: Endpoint
  // common
  method?: string
  path?: string
  status?: number  // HTTP/gRPC status code (gRPC: 0=OK)
  latencyMs: number
  requestId?: string
  isRetry?: boolean
  // request/response
  request: { headers: Record<string, string>; body?: string | null; size: number }
  response: { headers: Record<string, string>; body?: string | null; size: number }
  // gRPC
  grpcStatus?: string
  // DNS
  dnsQuery?: string
  dnsType?: string
  dnsRcode?: string
  dnsAnswer?: string[]
  dnsTtl?: number
}

export interface ClusterService {
  name: string
  ns: string
  kind: 'deployment' | 'statefulset' | 'external'
  ip: string
  port: number
  protocol: Protocol
}

export interface ClusterEdge {
  src: string
  dst: string
  proto: Protocol
  weight: number
}

export interface ClusterTopology {
  services: ClusterService[]
  edges: ClusterEdge[]
}

// Derived stats per service-edge for service-map and blast-radius
export interface EdgeStats {
  src: string
  dst: string
  proto: Protocol
  hits: number
  errors: number
  errorRate: number
  p50: number
  p95: number
  p99: number
}

export interface ServiceStats {
  service: string
  ns: string
  incoming: number
  outgoing: number
  errors: number
  errorRate: number
  p95: number
  protocol: Protocol
}

// Retry storm detection result
export interface RetryStorm {
  id: string
  requestId: string
  destination: string
  namespace: string
  path: string
  attempts: number
  firstSeen: number
  lastSeen: number
  durationMs: number
  statuses: number[]
  severity: 'critical' | 'high' | 'medium' | 'low'
  blastRadius: string[]  // services affected
}
