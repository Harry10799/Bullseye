// UI helpers — colors, formatting for the Kubeshark UI.
import type { TrafficEntry, Protocol } from '@/lib/traffic-types'

export function statusColor(status?: number, proto?: Protocol): string {
  if (status === undefined) return 'text-muted-foreground'
  if (proto === 'gRPC') {
    if (status === 0) return 'text-emerald-400'
    if (status >= 13) return 'text-rose-400'
    if (status >= 4) return 'text-amber-400'
    return 'text-cyan-400'
  }
  if (proto === 'DNS') {
    return status === 0 ? 'text-emerald-400' : 'text-rose-400'
  }
  if (status >= 500) return 'text-rose-400'
  if (status >= 400) return 'text-amber-400'
  if (status >= 300) return 'text-cyan-400'
  if (status >= 200) return 'text-emerald-400'
  return 'text-muted-foreground'
}

export function statusBg(status?: number, proto?: Protocol): string {
  if (status === undefined) return ''
  if (proto === 'gRPC' && status > 0) return 'bg-rose-500/10'
  if (proto === 'DNS' && status !== 0) return 'bg-rose-500/10'
  if (status >= 500) return 'bg-rose-500/10'
  if (status >= 400) return 'bg-amber-500/10'
  return ''
}

export function protocolBadge(proto: Protocol): { label: string; cls: string } {
  switch (proto) {
    case 'HTTP': return { label: 'HTTP', cls: 'bg-cyan-500/10 text-cyan-300 border-cyan-500/30' }
    case 'gRPC': return { label: 'gRPC', cls: 'bg-violet-500/10 text-violet-300 border-violet-500/30' }
    case 'DNS': return { label: 'DNS', cls: 'bg-amber-500/10 text-amber-300 border-amber-500/30' }
    case 'Kafka': return { label: 'Kafka', cls: 'bg-rose-500/10 text-rose-300 border-rose-500/30' }
    case 'Redis': return { label: 'Redis', cls: 'bg-emerald-500/10 text-emerald-300 border-emerald-500/30' }
    case 'Postgres': return { label: 'PG', cls: 'bg-sky-500/10 text-sky-300 border-sky-500/30' }
    default: return { label: String(proto), cls: 'bg-muted text-muted-foreground' }
  }
}

export function latencyColor(ms: number): string {
  if (ms >= 1000) return 'text-rose-400'
  if (ms >= 250) return 'text-amber-400'
  if (ms >= 100) return 'text-yellow-300/80'
  return 'text-emerald-400'
}

export function fmtTime(ts: number): string {
  const d = new Date(ts)
  return d.toLocaleTimeString('en-US', { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0')
}

export function fmtRelative(ts: number, now: number): string {
  const diff = now - ts
  if (diff < 1000) return `${diff}ms ago`
  if (diff < 60000) return `${(diff / 1000).toFixed(1)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  return `${Math.floor(diff / 3600000)}h ago`
}

export function fmtBytes(b?: number): string {
  if (!b) return '0 B'
  if (b < 1024) return `${b} B`
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`
  return `${(b / (1024 * 1024)).toFixed(2)} MB`
}

export function truncate(s: string, n: number): string {
  if (s.length <= n) return s
  return s.slice(0, n - 1) + '…'
}

export function prettyBody(body?: string | null): string {
  if (!body) return ''
  // try to pretty-print JSON
  try {
    const parsed = JSON.parse(body)
    return JSON.stringify(parsed, null, 2)
  } catch {
    return body
  }
}

export function highlightJson(json: string): string {
  // very lightweight syntax highlighter for display
  return json
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/("(?:[^"\\]|\\.)*"\s*:)/g, '<span style="color:oklch(0.78 0.16 80)">$1</span>')  // keys
    .replace(/:\s*("(?:[^"\\]|\\.)*")/g, ': <span style="color:oklch(0.75 0.18 145)">$1</span>')  // string values
    .replace(/:\s*(-?\d+\.?\d*)/g, ': <span style="color:oklch(0.7 0.15 180)">$1</span>')  // numbers
    .replace(/:\s*(true|false|null)/g, ': <span style="color:oklch(0.7 0.15 280)">$1</span>')  // keywords
}

export function entrySummary(e: TrafficEntry): string {
  if (e.protocol === 'DNS') return `${e.method} ${e.dnsQuery}`
  if (e.protocol === 'Redis') return `${e.method} ${e.path}`
  if (e.protocol === 'Kafka') return `${e.method} ${e.path}`
  return `${e.method} ${e.path}`
}
