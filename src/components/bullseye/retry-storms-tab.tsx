'use client'

import { useMemo, useState } from 'react'
import { RotateCw, AlertTriangle, Flame, TrendingUp, Clock, ChevronRight } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { useTrafficStore } from '@/store/traffic-store'
import { detectRetryStorms } from '@/lib/stats'
import type { RetryStorm } from '@/lib/traffic-types'
import { fmtTime } from '@/lib/ui'

export function RetryStormsTab() {
  const entries = useTrafficStore(s => s.entries)
  const [expanded, setExpanded] = useState<string | null>(null)

  const storms = useMemo(() => detectRetryStorms(entries), [entries])

  // related entries for the expanded storm
  const expandedEntries = useMemo(() => {
    if (!expanded) return []
    const storm = storms.find(s => s.id === expanded)
    if (!storm) return []
    return entries
      .filter(e => e.requestId === storm.requestId ||
        (e.destination.service === storm.destination && e.path === storm.path))
      .filter(e => e.timestamp >= storm.firstSeen - 100 && e.timestamp <= storm.lastSeen + 100)
      .sort((a, b) => a.timestamp - b.timestamp)
  }, [expanded, storms, entries])

  // severity counts
  const counts = useMemo(() => {
    return {
      critical: storms.filter(s => s.severity === 'critical').length,
      high: storms.filter(s => s.severity === 'high').length,
      medium: storms.filter(s => s.severity === 'medium').length,
      low: storms.filter(s => s.severity === 'low').length,
    }
  }, [storms])

  return (
    <div className="flex h-full min-h-0 overflow-auto scrollbar-thin">
      <div className="flex-1 p-4 space-y-4 min-w-0">
        {/* Header */}
        <div className="space-y-1">
          <div className="flex items-center gap-2">
            <Flame className="w-4 h-4 text-amber-400" />
            <span className="font-mono text-xs uppercase tracking-wider text-muted-foreground">retry storm detector</span>
          </div>
          <p className="text-sm text-muted-foreground">
            Detects clusters of repeated requests to the same destination within a 5-second window —
            usually a sign of misconfigured retry policies or cascading failures.
          </p>
        </div>

        {/* Summary tiles */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <StormStat label="active storms" value={storms.length} icon={<Flame className="w-4 h-4" />} tone="amber" />
          <StormStat label="critical" value={counts.critical} icon={<AlertTriangle className="w-4 h-4" />} tone="rose" />
          <StormStat label="high severity" value={counts.high} icon={<RotateCw className="w-4 h-4" />} tone="amber" />
          <StormStat label="total retries" value={storms.reduce((s, st) => s + st.attempts, 0)} icon={<TrendingUp className="w-4 h-4" />} tone="primary" />
        </div>

        {/* Storm list */}
        {storms.length === 0 ? (
          <Card className="bg-card/30 border-border">
            <CardContent className="p-8 text-center">
              <div className="w-12 h-12 mx-auto rounded-full bg-emerald-500/10 border border-emerald-500/30 flex items-center justify-center mb-3">
                <RotateCw className="w-5 h-5 text-emerald-400" />
              </div>
              <p className="text-sm font-mono text-emerald-300">No retry storms detected</p>
              <p className="text-xs text-muted-foreground mt-1">
                Cluster retry patterns look healthy. Storms will appear here when 3+ identical requests hit the same destination within 5s.
              </p>
            </CardContent>
          </Card>
        ) : (
          <div className="space-y-2">
            {storms.map(storm => (
              <StormCard
                key={storm.id}
                storm={storm}
                expanded={expanded === storm.id}
                onToggle={() => setExpanded(expanded === storm.id ? null : storm.id)}
                relatedEntries={expanded === storm.id ? expandedEntries : []}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function StormStat({ label, value, icon, tone }: { label: string; value: number; icon: React.ReactNode; tone: 'rose' | 'amber' | 'primary' }) {
  const toneCls = {
    rose: 'text-rose-300',
    amber: 'text-amber-300',
    primary: 'text-primary',
  }[tone]
  return (
    <Card className="bg-card/30 border-border">
      <CardContent className="p-3">
        <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-muted-foreground font-mono mb-1">
          {icon}{label}
        </div>
        <div className={`font-mono text-xl font-bold ${toneCls}`}>{value}</div>
      </CardContent>
    </Card>
  )
}

function StormCard({ storm, expanded, onToggle, relatedEntries }: {
  storm: RetryStorm
  expanded: boolean
  onToggle: () => void
  relatedEntries: any[]
}) {
  const severityCfg = {
    critical: { label: 'CRIT', cls: 'border-rose-500/50 text-rose-300 bg-rose-500/15' },
    high: { label: 'HIGH', cls: 'border-amber-500/50 text-amber-300 bg-amber-500/15' },
    medium: { label: 'MED', cls: 'border-yellow-500/50 text-yellow-300 bg-yellow-500/15' },
    low: { label: 'LOW', cls: 'border-sky-500/50 text-sky-300 bg-sky-500/15' },
  }[storm.severity]

  const failedStatuses = storm.statuses.filter(s => s >= 500 || s > 0)
  const failedPct = storm.attempts ? (failedStatuses.length / storm.attempts) * 100 : 0

  return (
    <Card className="bg-card/30 border-border overflow-hidden">
      <button
        onClick={onToggle}
        className="w-full text-left p-3 hover:bg-muted/30 transition-colors"
      >
        <div className="flex items-center gap-3">
          <Badge variant="outline" className={`font-mono text-[10px] ${severityCfg.cls}`}>
            {severityCfg.label}
          </Badge>
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2">
              <RotateCw className="w-3.5 h-3.5 text-amber-400 shrink-0" />
              <span className="font-mono text-sm font-semibold truncate">{storm.destination}</span>
              <span className="text-[10px] font-mono text-muted-foreground">{storm.namespace}</span>
            </div>
            <div className="font-mono text-xs text-muted-foreground truncate mt-0.5">
              {storm.path || '(no path)'} · req-id: {storm.requestId}
            </div>
          </div>
          <div className="flex items-center gap-3 shrink-0">
            <div className="text-right">
              <div className="font-mono text-lg font-bold text-amber-300">{storm.attempts}</div>
              <div className="text-[9px] uppercase tracking-wider text-muted-foreground font-mono">attempts</div>
            </div>
            <ChevronRight className={`w-4 h-4 text-muted-foreground transition-transform ${expanded ? 'rotate-90' : ''}`} />
          </div>
        </div>

        <div className="flex items-center gap-4 mt-2 text-[10px] font-mono text-muted-foreground">
          <span className="flex items-center gap-1">
            <Clock className="w-3 h-3" />
            {fmtTime(storm.firstSeen)} → {fmtTime(storm.lastSeen)}
          </span>
          <span>duration: {storm.durationMs}ms</span>
          <span className={failedPct > 80 ? 'text-rose-300' : failedPct > 50 ? 'text-amber-300' : 'text-emerald-300'}>
            {failedPct.toFixed(0)}% failed
          </span>
          <span>status codes: [{storm.statuses.join(', ')}]</span>
        </div>
      </button>

      {expanded && (
        <div className="border-t border-border bg-muted/10 p-3 space-y-3">
          {/* Blast radius from this storm */}
          <div>
            <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono mb-1.5">potential blast radius</div>
            <div className="flex flex-wrap gap-1.5">
              {storm.blastRadius.map(svc => (
                <Badge key={svc} variant="outline" className="font-mono text-[10px] border-amber-500/30 text-amber-300 bg-amber-500/10">
                  {svc}
                </Badge>
              ))}
              <span className="text-[10px] font-mono text-muted-foreground self-center">
                · {storm.blastRadius.length} services touched
              </span>
            </div>
          </div>

          {/* Timeline of attempts */}
          <div>
            <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono mb-1.5">
              attempt timeline ({relatedEntries.length} packets captured)
            </div>
            <div className="space-y-1 max-h-[260px] overflow-auto scrollbar-thin">
              {relatedEntries.map((e, i) => (
                <div
                  key={e.id}
                  className="grid grid-cols-[24px_110px_60px_1fr_60px_70px] gap-2 items-center px-2 py-1 rounded border border-border bg-muted/20 text-[11px] font-mono"
                >
                  <span className="text-muted-foreground text-right">{i + 1}.</span>
                  <span className="text-muted-foreground tabular-nums">{fmtTime(e.timestamp)}</span>
                  <span className="text-cyan-300">{e.method}</span>
                  <span className="text-foreground truncate">{e.path}</span>
                  <span className={`text-right ${e.status >= 500 ? 'text-rose-300' : e.status >= 400 ? 'text-amber-300' : 'text-emerald-300'}`}>
                    {e.status}
                  </span>
                  <span className="text-right text-muted-foreground">{e.latencyMs}ms</span>
                </div>
              ))}
              {relatedEntries.length === 0 && (
                <div className="text-xs text-muted-foreground font-mono italic">No matching packets found in current buffer</div>
              )}
            </div>
          </div>

          {/* Recommended actions */}
          <div className="pt-2 border-t border-border">
            <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono mb-1.5">recommended actions</div>
            <ul className="space-y-1 text-xs text-foreground/90">
              <li>• Check retry policy on <code className="font-mono text-primary">{storm.blastRadius[0]}</code> — caps may be too high</li>
              <li>• Add exponential backoff with jitter to {storm.destination} calls</li>
              <li>• Verify {storm.destination} health — circuit breaker may be open</li>
              <li>• Consider failing fast on {storm.statuses.filter(s => s >= 500).length > 0 ? '5xx' : '4xx'} instead of retrying</li>
            </ul>
          </div>
        </div>
      )}
    </Card>
  )
}
