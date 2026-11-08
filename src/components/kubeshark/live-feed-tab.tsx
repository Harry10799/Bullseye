'use client'

import { useEffect, useMemo, useState, useRef } from 'react'
import { Search, X, ChevronDown, Filter, Zap, AlertCircle, CheckCircle2 } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { useTrafficStore } from '@/store/traffic-store'
import { parseFilter, applyFilter } from '@/lib/filter-engine'
import type { TrafficEntry } from '@/lib/traffic-types'
import {
  statusColor, statusBg, protocolBadge, latencyColor, fmtTime, entrySummary,
} from '@/lib/ui'
import { DetailPanel } from './detail-panel'

const QUICK_FILTERS: { label: string; expr: string }[] = [
  { label: 'errors 5xx', expr: 'status >= 500' },
  { label: '4xx', expr: 'status >= 400 and status < 500' },
  { label: 'slow > 1s', expr: 'latency > 1000' },
  { label: 'payments', expr: 'svc contains "payments"' },
  { label: 'DNS', expr: 'protocol == "DNS"' },
  { label: 'NXDOMAIN', expr: 'dns.rcode == "NXDOMAIN"' },
  { label: 'gRPC errors', expr: 'protocol == "gRPC" and status > 0' },
  { label: 'TLS', expr: 'tls == true' },
]

export function LiveFeedTab() {
  const entries = useTrafficStore(s => s.entries)
  const selectedId = useTrafficStore(s => s.selectedId)
  const select = useTrafficStore(s => s.select)
  const [filterInput, setFilterInput] = useState('')
  const [filterExpr, setFilterExpr] = useState('')
  const [autoScroll, setAutoScroll] = useState(true)
  const listRef = useRef<HTMLDivElement>(null)

  // debounce filter compilation
  useEffect(() => {
    const t = setTimeout(() => setFilterExpr(filterInput), 200)
    return () => clearTimeout(t)
  }, [filterInput])

  const filterResult = useMemo(() => parseFilter(filterExpr), [filterExpr])

  const filtered = useMemo(() => {
    if (!filterResult.ok || !filterResult.ast) return entries
    return entries.filter(e => applyFilter(e, filterResult.ast))
  }, [entries, filterResult])

  // cap rows shown to keep DOM light
  const visible = filtered.slice(0, 400)

  useEffect(() => {
    if (autoScroll && listRef.current) {
      listRef.current.scrollTop = 0
    }
  }, [visible, autoScroll])

  return (
    <div className="flex h-full min-h-0">
      {/* Left: filter + table */}
      <div className="flex-1 flex flex-col min-w-0">
        <FilterBar
          value={filterInput}
          onChange={setFilterInput}
          result={filterResult}
          matchCount={filtered.length}
          totalCount={entries.length}
          autoScroll={autoScroll}
          setAutoScroll={setAutoScroll}
        />

        {/* Table header */}
        <div className="grid grid-cols-[110px_60px_180px_180px_70px_1fr_60px_70px] gap-2 px-3 py-2 border-b border-border bg-muted/30 text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
          <div>time</div>
          <div>proto</div>
          <div>source → svc</div>
          <div>destination → svc</div>
          <div>method</div>
          <div>path / query</div>
          <div className="text-right">status</div>
          <div className="text-right">latency</div>
        </div>

        {/* Rows */}
        <div
          ref={listRef}
          className="flex-1 overflow-auto scrollbar-thin min-h-0"
        >
          {visible.length === 0 ? (
            <div className="p-8 text-center text-sm text-muted-foreground">
              {filterResult.ok === false ? (
                <span className="text-rose-400">Filter error: {filterResult.error}</span>
              ) : (
                'No packets match the current filter'
              )}
            </div>
          ) : (
            visible.map(e => (
              <FeedRow
                key={e.id}
                entry={e}
                selected={e.id === selectedId}
                onClick={() => select(e.id === selectedId ? null : e.id)}
              />
            ))
          )}
        </div>
      </div>

      {/* Right: detail panel */}
      <aside className="w-[420px] border-l border-border flex-shrink-0 hidden lg:flex flex-col">
        <DetailPanel entryId={selectedId} />
      </aside>
    </div>
  )
}

function FilterBar({ value, onChange, result, matchCount, totalCount, autoScroll, setAutoScroll }: {
  value: string
  onChange: (s: string) => void
  result: { ok: boolean; error?: string }
  matchCount: number
  totalCount: number
  autoScroll: boolean
  setAutoScroll: (b: boolean) => void
}) {
  const [showQuick, setShowQuick] = useState(false)

  return (
    <div className="border-b border-border bg-card/30 px-3 py-2 space-y-2">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Filter className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground pointer-events-none" />
          <Input
            value={value}
            onChange={e => onChange(e.target.value)}
            placeholder='filter:  src.pod == "payments*" and status >= 500   (try: protocol == "DNS")'
            className={`h-9 pl-8 pr-8 font-mono text-xs ${
              result.ok === false
                ? 'border-rose-500/50 focus-visible:border-rose-500'
                : value
                ? 'border-primary/40 focus-visible:border-primary'
                : ''
            }`}
            onKeyDown={e => {
              if (e.key === 'Escape') onChange('')
            }}
          />
          {value && (
            <button
              onClick={() => onChange('')}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          )}
        </div>

        <Button
          variant="outline"
          size="sm"
          className="h-9 gap-1.5 font-mono text-xs"
          onClick={() => setShowQuick(s => !s)}
        >
          <Zap className="w-3.5 h-3.5" />
          quick
          <ChevronDown className="w-3 h-3" />
        </Button>

        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant={autoScroll ? 'default' : 'outline'}
                size="sm"
                className="h-9 gap-1.5 font-mono text-xs"
                onClick={() => setAutoScroll(!autoScroll)}
              >
                {autoScroll ? <CheckCircle2 className="w-3.5 h-3.5" /> : <AlertCircle className="w-3.5 h-3.5" />}
                {autoScroll ? 'live' : 'paused'}
              </Button>
            </TooltipTrigger>
            <TooltipContent>Toggle auto-scroll to newest</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </div>

      {/* Status row */}
      <div className="flex items-center justify-between text-[11px] font-mono">
        <div className="flex items-center gap-2">
          {result.ok === false ? (
            <Badge variant="outline" className="gap-1 border-rose-500/40 text-rose-300 bg-rose-500/10">
              <AlertCircle className="w-3 h-3" /> parse error: {result.error}
            </Badge>
          ) : value ? (
            <Badge variant="outline" className="gap-1 border-emerald-500/40 text-emerald-300 bg-emerald-500/10">
              <CheckCircle2 className="w-3 h-3" /> {matchCount} / {totalCount} matched
            </Badge>
          ) : (
            <span className="text-muted-foreground">{totalCount} packets captured · showing latest 400</span>
          )}
        </div>
        <span className="text-muted-foreground/60 hidden sm:inline">
          fields: src.pod · dst.svc · status · latency · protocol · dns.rcode · path
        </span>
      </div>

      {/* Quick filters */}
      {showQuick && (
        <div className="flex flex-wrap gap-1.5 pt-1 border-t border-border">
          {QUICK_FILTERS.map(q => (
            <button
              key={q.label}
              onClick={() => onChange(q.expr)}
              className="text-[11px] font-mono px-2 py-1 rounded border border-border bg-muted/40 hover:bg-primary/10 hover:border-primary/40 hover:text-primary transition-colors"
            >
              {q.label} <span className="text-muted-foreground/70 ml-1">{q.expr}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function FeedRow({ entry, selected, onClick }: { entry: TrafficEntry; selected: boolean; onClick: () => void }) {
  const proto = protocolBadge(entry.protocol)
  const isError =
    (entry.protocol === 'HTTP' && (entry.status ?? 0) >= 400) ||
    (entry.protocol === 'gRPC' && (entry.status ?? 0) > 0) ||
    (entry.protocol === 'DNS' && entry.dnsRcode !== 'NOERROR')

  return (
    <button
      onClick={onClick}
      className={`w-full text-left grid grid-cols-[110px_60px_180px_180px_70px_1fr_60px_70px] gap-2 px-3 py-1.5 border-b border-border/50 text-xs font-mono transition-colors ${
        selected
          ? 'bg-primary/15 border-l-2 border-l-primary'
          : isError
          ? `${statusBg(entry.status, entry.protocol)} hover:bg-rose-500/15`
          : 'hover:bg-muted/40'
      }`}
    >
      <div className="text-muted-foreground tabular-nums truncate">{fmtTime(entry.timestamp)}</div>
      <div>
        <Badge variant="outline" className={`text-[9px] px-1 py-0 h-4 border ${proto.cls} font-mono`}>
          {proto.label}
        </Badge>
      </div>
      <div className="truncate">
        <div className="text-foreground/90 truncate">{entry.source.service}</div>
        <div className="text-muted-foreground/70 text-[10px] truncate">{entry.source.ns}</div>
      </div>
      <div className="truncate">
        <div className="text-foreground/90 truncate">{entry.destination.service}</div>
        <div className="text-muted-foreground/70 text-[10px] truncate">{entry.destination.ns}</div>
      </div>
      <div className="text-foreground/80 truncate">{entry.method || '—'}</div>
      <div className="text-foreground truncate">
        {entry.protocol === 'DNS' ? entry.dnsQuery : entry.path || '—'}
        {entry.isRetry && <span className="ml-1.5 text-[9px] text-amber-400">↻retry</span>}
      </div>
      <div className={`text-right tabular-nums ${statusColor(entry.status, entry.protocol)}`}>
        {entry.protocol === 'gRPC' ? (entry.status === 0 ? 'OK' : entry.status) : (entry.status ?? '—')}
      </div>
      <div className={`text-right tabular-nums ${latencyColor(entry.latencyMs)}`}>
        {entry.latencyMs < 1000 ? `${entry.latencyMs}ms` : `${(entry.latencyMs / 1000).toFixed(2)}s`}
      </div>
    </button>
  )
}
