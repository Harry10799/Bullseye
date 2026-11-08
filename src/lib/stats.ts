// Stats helpers — compute service-map stats, blast radius, retry storms.
import type { TrafficEntry, EdgeStats, ServiceStats, RetryStorm, ClusterTopology } from './traffic-types'

export function isHttpError(e: TrafficEntry): boolean {
  if (e.protocol === 'HTTP') return (e.status ?? 0) >= 400
  if (e.protocol === 'gRPC') return (e.status ?? 0) > 0
  if (e.protocol === 'DNS') return e.dnsRcode !== 'NOERROR'
  return false
}

function percentile(sorted: number[], p: number): number {
  if (sorted.length === 0) return 0
  const idx = Math.min(sorted.length - 1, Math.floor((p / 100) * sorted.length))
  return sorted[idx]
}

export function computeEdgeStats(entries: TrafficEntry[]): EdgeStats[] {
  const m = new Map<string, TrafficEntry[]>()
  for (const e of entries) {
    const key = `${e.source.service}|${e.destination.service}`
    if (!m.has(key)) m.set(key, [])
    m.get(key)!.push(e)
  }
  const out: EdgeStats[] = []
  for (const [key, list] of m.entries()) {
    const [src, dst] = key.split('|')
    const latencies = list.map(e => e.latencyMs).sort((a, b) => a - b)
    const errors = list.filter(isHttpError).length
    out.push({
      src, dst,
      proto: list[0].protocol,
      hits: list.length,
      errors,
      errorRate: list.length ? errors / list.length : 0,
      p50: percentile(latencies, 50),
      p95: percentile(latencies, 95),
      p99: percentile(latencies, 99),
    })
  }
  return out.sort((a, b) => b.hits - a.hits)
}

export function computeServiceStats(entries: TrafficEntry[], topology?: ClusterTopology): ServiceStats[] {
  const m = new Map<string, { incoming: number; outgoing: number; errors: number; latencies: number[]; ns: string; proto: any }>()
  const ensure = (svc: string, ns: string, proto: any) => {
    if (!m.has(svc)) m.set(svc, { incoming: 0, outgoing: 0, errors: 0, latencies: [], ns, proto })
    return m.get(svc)!
  }
  for (const e of entries) {
    const src = ensure(e.source.service, e.source.ns, e.protocol)
    src.outgoing++
    src.latencies.push(e.latencyMs)
    if (isHttpError(e)) src.errors++
    const dst = ensure(e.destination.service, e.destination.ns, e.protocol)
    dst.incoming++
    dst.latencies.push(e.latencyMs)
  }
  // backfill namespace from topology if missing
  if (topology) {
    for (const svc of topology.services) {
      ensure(svc.name, svc.ns, svc.protocol)
    }
  }
  const out: ServiceStats[] = []
  for (const [service, v] of m.entries()) {
    const sorted = [...v.latencies].sort((a, b) => a - b)
    const total = v.incoming + v.outgoing
    out.push({
      service,
      ns: v.ns,
      incoming: v.incoming,
      outgoing: v.outgoing,
      errors: v.errors,
      errorRate: total ? v.errors / total : 0,
      p95: percentile(sorted, 95),
      protocol: v.proto,
    })
  }
  return out.sort((a, b) => (b.incoming + b.outgoing) - (a.incoming + a.outgoing))
}

// blast radius — given a failing service, who is impacted transitively?
// build reverse graph: who calls X?
export function computeBlastRadius(entries: TrafficEntry[], failingService: string): {
  impacted: { service: string; ns: string; path: string[]; errorRate: number; calls: number }[]
  root: { service: string; ns: string; errorRate: number; calls: number }
} {
  // reverse callers — caller -> set of callees
  const callers = new Map<string, Set<string>>()
  const nsOf = new Map<string, string>()
  const errCount = new Map<string, number>()
  const totalCount = new Map<string, number>()

  for (const e of entries) {
    if (!callers.has(e.destination.service)) callers.set(e.destination.service, new Set())
    callers.get(e.destination.service)!.add(e.source.service)
    nsOf.set(e.source.service, e.source.ns)
    nsOf.set(e.destination.service, e.destination.ns)
    const k = e.destination.service
    totalCount.set(k, (totalCount.get(k) || 0) + 1)
    if (isHttpError(e)) errCount.set(k, (errCount.get(k) || 0) + 1)
  }

  const rootErrRate = totalCount.get(failingService)
    ? (errCount.get(failingService) || 0) / totalCount.get(failingService)!
    : 0

  const impacted: { service: string; ns: string; path: string[]; errorRate: number; calls: number }[] = []
  const visited = new Set<string>([failingService])

  // BFS from failingService in the reverse graph
  const queue: { svc: string; path: string[] }[] = [{ svc: failingService, path: [failingService] }]
  while (queue.length) {
    const { svc, path } = queue.shift()!
    const callersOfSvc = callers.get(svc) || new Set()
    for (const caller of callersOfSvc) {
      if (visited.has(caller)) continue
      visited.add(caller)
      const newPath = [caller, ...path]
      const errRate = totalCount.get(caller)
        ? (errCount.get(caller) || 0) / totalCount.get(caller)!
        : 0
      impacted.push({
        service: caller,
        ns: nsOf.get(caller) || '',
        path: newPath,
        errorRate: errRate,
        calls: totalCount.get(caller) || 0,
      })
      queue.push({ svc: caller, path: newPath })
    }
  }

  return {
    impacted: impacted.sort((a, b) => b.calls - a.calls),
    root: {
      service: failingService,
      ns: nsOf.get(failingService) || '',
      errorRate: rootErrRate,
      calls: totalCount.get(failingService) || 0,
    },
  }
}

// retry storm detector — cluster entries with same requestId (or same dst+method+path within 2s)
export function detectRetryStorms(entries: TrafficEntry[]): RetryStorm[] {
  // bucket by requestId (when present) or by `${dst.svc}|${method}|${path}`
  const buckets = new Map<string, TrafficEntry[]>()
  for (const e of entries) {
    if (e.protocol !== 'HTTP' && e.protocol !== 'gRPC') continue
    const key = e.requestId
      ? `rid|${e.requestId}`
      : `${e.destination.service}|${e.method || ''}|${e.path || ''}`
    if (!buckets.has(key)) buckets.set(key, [])
    buckets.get(key)!.push(e)
  }

  const storms: RetryStorm[] = []
  for (const [key, list] of buckets.entries()) {
    if (list.length < 3) continue
    // cluster within 5-second window
    const sorted = list.sort((a, b) => a.timestamp - b.timestamp)
    let window: TrafficEntry[] = [sorted[0]]
    for (let i = 1; i < sorted.length; i++) {
      if (sorted[i].timestamp - window[0].timestamp <= 5000) {
        window.push(sorted[i])
      } else {
        if (window.length >= 3) storms.push(buildStorm(window, key))
        window = [sorted[i]]
      }
    }
    if (window.length >= 3) storms.push(buildStorm(window, key))
  }

  return storms.sort((a, b) => b.attempts - a.attempts).slice(0, 30)
}

function buildStorm(list: TrafficEntry[], key: string): RetryStorm {
  const first = list[0]
  const last = list[list.length - 1]
  const statuses = list.map(e => e.status || 0)
  const failed = statuses.filter(s => s >= 500 || s > 0 && list[0].protocol === 'gRPC').length
  const severity =
    list.length >= 8 ? 'critical' :
    list.length >= 5 ? 'high' :
    list.length >= 3 ? 'medium' : 'low'

  // blast radius — who depends on the source service?
  const blastRadius = [first.source.service, first.destination.service]

  return {
    id: `storm_${key}_${first.timestamp}`,
    requestId: first.requestId || '(no-rid)',
    destination: first.destination.service,
    namespace: first.destination.ns,
    path: first.path || first.method || '',
    attempts: list.length,
    firstSeen: first.timestamp,
    lastSeen: last.timestamp,
    durationMs: last.timestamp - first.timestamp,
    statuses,
    severity: severity as any,
    blastRadius,
  }
}
