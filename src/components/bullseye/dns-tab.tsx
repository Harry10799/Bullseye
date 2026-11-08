'use client'

import { useMemo, useState } from 'react'
import { Globe, Search, X, AlertTriangle, CheckCircle2 } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { useTrafficStore } from '@/store/traffic-store'
import type { TrafficEntry } from '@/lib/traffic-types'
import { fmtTime } from '@/lib/ui'

export function DnsTab() {
  const entries = useTrafficStore(s => s.entries)
  const selectedId = useTrafficStore(s => s.selectedId)
  const select = useTrafficStore(s => s.select)
  const [search, setSearch] = useState('')
  const [showNxOnly, setShowNxOnly] = useState(false)

  const dnsEntries = useMemo(() => {
    return entries
      .filter(e => e.protocol === 'DNS')
      .filter(e => !showNxOnly || e.dnsRcode === 'NXDOMAIN')
      .filter(e => {
        if (!search) return true
        const q = (e.dnsQuery || '').toLowerCase()
        return q.includes(search.toLowerCase())
      })
      .slice(0, 500)
  }, [entries, search, showNxOnly])

  const stats = useMemo(() => {
    const all = entries.filter(e => e.protocol === 'DNS')
    const nx = all.filter(e => e.dnsRcode === 'NXDOMAIN').length
    const ok = all.length - nx
    return { total: all.length, nx, ok }
  }, [entries])

  return (
    <div className="flex h-full min-h-0">
      <div className="flex-1 flex flex-col min-w-0">
        {/* Filter bar */}
        <div className="border-b border-border bg-card/30 px-3 py-2 space-y-2">
          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground" />
              <Input
                value={search}
                onChange={e => setSearch(e.target.value)}
                placeholder="filter by query name (e.g. payments-api.payments.svc.cluster.local)"
                className="h-9 pl-8 pr-8 font-mono text-xs"
              />
              {search && (
                <button onClick={() => setSearch('')} className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground">
                  <X className="w-3.5 h-3.5" />
                </button>
              )}
            </div>
            <button
              onClick={() => setShowNxOnly(!showNxOnly)}
              className={`flex items-center gap-1.5 h-9 px-3 rounded border font-mono text-xs transition-colors ${
                showNxOnly
                  ? 'border-rose-500/40 text-rose-300 bg-rose-500/10'
                  : 'border-border bg-muted/30 hover:bg-muted/50'
              }`}
            >
              <AlertTriangle className="w-3.5 h-3.5" />
              NXDOMAIN only
            </button>
          </div>

          <div className="flex items-center gap-2 text-[11px] font-mono">
            <Badge variant="outline" className="gap-1 border-emerald-500/40 text-emerald-300 bg-emerald-500/10">
              <CheckCircle2 className="w-3 h-3" /> {stats.ok} NOERROR
            </Badge>
            <Badge variant="outline" className="gap-1 border-rose-500/40 text-rose-300 bg-rose-500/10">
              <AlertTriangle className="w-3 h-3" /> {stats.nx} NXDOMAIN
            </Badge>
            <span className="text-muted-foreground">·</span>
            <span className="text-muted-foreground">{dnsEntries.length} shown</span>
          </div>
        </div>

        {/* Table header */}
        <div className="grid grid-cols-[110px_1fr_120px_100px_80px_60px] gap-2 px-3 py-2 border-b border-border bg-muted/30 text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
          <div>time</div>
          <div>query</div>
          <div>type</div>
          <div>answer</div>
          <div className="text-right">ttl</div>
          <div className="text-right">rcode</div>
        </div>

        {/* Rows */}
        <div className="flex-1 overflow-auto scrollbar-thin min-h-0">
          {dnsEntries.length === 0 ? (
            <div className="p-8 text-center text-sm text-muted-foreground font-mono">
              No DNS queries match the current filter
            </div>
          ) : (
            dnsEntries.map(e => (
              <DnsRow
                key={e.id}
                entry={e}
                selected={e.id === selectedId}
                onClick={() => select(e.id === selectedId ? null : e.id)}
              />
            ))
          )}
        </div>
      </div>

      <aside className="w-[420px] border-l border-border flex-shrink-0 hidden lg:flex flex-col bg-card/30">
        <DnsDetail entryId={selectedId} />
      </aside>
    </div>
  )
}

function DnsRow({ entry, selected, onClick }: { entry: TrafficEntry; selected: boolean; onClick: () => void }) {
  const isNx = entry.dnsRcode === 'NXDOMAIN'
  return (
    <button
      onClick={onClick}
      className={`w-full text-left grid grid-cols-[110px_1fr_120px_100px_80px_60px] gap-2 px-3 py-1.5 border-b border-border/50 text-xs font-mono transition-colors ${
        selected
          ? 'bg-primary/15 border-l-2 border-l-primary'
          : isNx
          ? 'bg-rose-500/5 hover:bg-rose-500/15'
          : 'hover:bg-muted/40'
      }`}
    >
      <div className="text-muted-foreground tabular-nums">{fmtTime(entry.timestamp)}</div>
      <div className="truncate text-foreground">{entry.dnsQuery}</div>
      <div className="text-muted-foreground">{entry.dnsType}</div>
      <div className={`truncate ${isNx ? 'text-rose-400' : 'text-emerald-300'}`}>
        {isNx ? '—' : (entry.dnsAnswer && entry.dnsAnswer[0]) || '—'}
      </div>
      <div className="text-right text-muted-foreground">{entry.dnsTtl || 0}s</div>
      <div className={`text-right ${isNx ? 'text-rose-400' : 'text-emerald-400'}`}>
        {entry.dnsRcode}
      </div>
    </button>
  )
}

function DnsDetail({ entryId }: { entryId: string | null }) {
  const entries = useTrafficStore(s => s.entries)
  const entry = entryId ? entries.find(e => e.id === entryId && e.protocol === 'DNS') : undefined

  if (!entry) {
    return (
      <div className="h-full flex items-center justify-center text-center px-6">
        <div className="space-y-2">
          <div className="w-12 h-12 mx-auto rounded-full bg-muted/50 flex items-center justify-center">
            <Globe className="w-5 h-5 text-muted-foreground" />
          </div>
          <p className="text-sm text-muted-foreground">Select a DNS query to inspect</p>
          <p className="text-xs text-muted-foreground/70">View query, answer, rcode, TTL, source pod</p>
        </div>
      </div>
    )
  }

  const isNx = entry.dnsRcode === 'NXDOMAIN'

  return (
    <div className="p-4 space-y-3 overflow-auto scrollbar-thin">
      <div className="flex items-center justify-between">
        <Badge variant="outline" className="gap-1 border-amber-500/40 text-amber-300 bg-amber-500/10 font-mono text-[11px]">
          <Globe className="w-3 h-3" /> DNS
        </Badge>
        <Badge variant="outline" className={`font-mono text-[11px] ${isNx ? 'border-rose-500/40 text-rose-300 bg-rose-500/10' : 'border-emerald-500/40 text-emerald-300 bg-emerald-500/10'}`}>
          {entry.dnsRcode}
        </Badge>
      </div>

      <div className="space-y-2">
        <Field label="query" value={entry.dnsQuery || '—'} mono highlight />
        <Field label="type" value={entry.dnsType || '—'} mono />
        <Field label="rcode" value={entry.dnsRcode || '—'} mono valueClass={isNx ? 'text-rose-400' : 'text-emerald-400'} />
        <Field label="ttl" value={`${entry.dnsTtl || 0}s`} mono />
        <Field label="latency" value={`${entry.latencyMs}ms`} mono />
      </div>

      <div>
        <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono mb-1.5">answer</div>
        <div className="rounded border border-border bg-muted/20 p-3 font-mono text-xs">
          {isNx ? (
            <span className="text-rose-400 italic">No DNS records found (NXDOMAIN)</span>
          ) : entry.dnsAnswer && entry.dnsAnswer.length > 0 ? (
            <div className="space-y-1">
              {entry.dnsAnswer.map((a, i) => (
                <div key={i} className="text-emerald-300">{a}</div>
              ))}
            </div>
          ) : (
            <span className="text-muted-foreground italic">(empty)</span>
          )}
        </div>
      </div>

      <div className="pt-2 border-t border-border">
        <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono mb-1.5">resolver</div>
        <div className="font-mono text-xs space-y-1 bg-muted/30 rounded p-2">
          <div><span className="text-muted-foreground">pod:</span> <span className="text-foreground">{entry.source.pod}</span></div>
          <div><span className="text-muted-foreground">svc:</span> <span className="text-foreground">{entry.source.service}</span></div>
          <div><span className="text-muted-foreground">ns:</span> <span className="text-foreground">{entry.source.ns}</span></div>
          <div><span className="text-muted-foreground">dns server:</span> <span className="text-foreground">{entry.destination.ip}:{entry.destination.port}</span></div>
        </div>
      </div>
    </div>
  )
}

function Field({ label, value, mono, highlight, valueClass }: {
  label: string; value: string; mono?: boolean; highlight?: boolean; valueClass?: string
}) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-[10px] uppercase tracking-wider text-muted-foreground shrink-0">{label}</span>
      <span className={`${mono ? 'font-mono' : ''} ${highlight ? 'text-primary' : ''} ${valueClass || ''} text-right break-all`}>
        {value}
      </span>
    </div>
  )
}
