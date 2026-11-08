'use client'

import { useMemo, useState } from 'react'
import { Zap, AlertOctagon, ArrowDown, GitBranch, Activity } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useTrafficStore } from '@/store/traffic-store'
import { computeBlastRadius, isHttpError } from '@/lib/stats'

export function BlastRadiusTab() {
  const entries = useTrafficStore(s => s.entries)
  const topology = useTrafficStore(s => s.topology)

  // candidate services — those with incoming traffic (i.e. someone depends on them)
  const candidates = useMemo(() => {
    const incomingCount = new Map<string, number>()
    for (const e of entries) {
      incomingCount.set(e.destination.service, (incomingCount.get(e.destination.service) || 0) + 1)
    }
    const list = Array.from(incomingCount.entries())
      .map(([svc, count]) => ({ svc, count }))
      .sort((a, b) => b.count - a.count)
    return list
  }, [entries])

  // pick the service with most errors by default, or the first candidate
  const defaultService = useMemo(() => {
    const errBySvc = new Map<string, number>()
    for (const e of entries) {
      if (isHttpError(e)) {
        errBySvc.set(e.destination.service, (errBySvc.get(e.destination.service) || 0) + 1)
      }
    }
    return Array.from(errBySvc.entries()).sort((a, b) => b[1] - a[1])[0]?.[0] || candidates[0]?.svc || ''
  }, [entries, candidates])

  const [selected, setSelected] = useState<string>(defaultService)
  const effectiveSelected = selected || defaultService

  const blast = useMemo(
    () => computeBlastRadius(entries, effectiveSelected),
    [entries, effectiveSelected]
  )

  // overall cluster impact metrics
  const totalErrors = entries.filter(isHttpError).length
  const totalCalls = entries.length
  const clusterErrorRate = totalCalls ? (totalErrors / totalCalls) * 100 : 0

  // Find direct callers for visualization tree
  const directCallers = blast.impacted.filter(p => p.path.length === 2)
  const transitiveCallers = blast.impacted.filter(p => p.path.length > 2)

  return (
    <div className="flex h-full min-h-0 overflow-auto scrollbar-thin">
      <div className="flex-1 p-4 space-y-4 min-w-0">
        {/* Header */}
        <div className="flex items-center justify-between gap-4">
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <Zap className="w-4 h-4 text-rose-400" />
              <span className="font-mono text-xs uppercase tracking-wider text-muted-foreground">blast radius analysis</span>
            </div>
            <p className="text-sm text-muted-foreground">
              Pick a failing service — see which upstream services would be impacted if it goes down.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <span className="text-[11px] font-mono text-muted-foreground">failing service:</span>
            <Select value={effectiveSelected} onValueChange={setSelected}>
              <SelectTrigger className="h-9 w-[230px] font-mono text-xs">
                <SelectValue placeholder="select service" />
              </SelectTrigger>
              <SelectContent>
                {candidates.map(c => (
                  <SelectItem key={c.svc} value={c.svc} className="font-mono text-xs">
                    {c.svc} <span className="text-muted-foreground ml-1">({c.count} calls)</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        {/* Top metrics */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <MetricCard
            label="impacted services"
            value={String(blast.impacted.length)}
            icon={<AlertOctagon className="w-4 h-4" />}
            tone={blast.impacted.length > 3 ? 'rose' : blast.impacted.length > 0 ? 'amber' : 'emerald'}
          />
          <MetricCard
            label="root error rate"
            value={(blast.root.errorRate * 100).toFixed(1) + '%'}
            icon={<Activity className="w-4 h-4" />}
            tone={blast.root.errorRate > 0.1 ? 'rose' : blast.root.errorRate > 0.03 ? 'amber' : 'emerald'}
          />
          <MetricCard
            label="root calls"
            value={String(blast.root.calls)}
            icon={<GitBranch className="w-4 h-4" />}
            tone="primary"
          />
          <MetricCard
            label="cluster error rate"
            value={clusterErrorRate.toFixed(2) + '%'}
            icon={<AlertOctagon className="w-4 h-4" />}
            tone={clusterErrorRate > 5 ? 'rose' : clusterErrorRate > 1 ? 'amber' : 'emerald'}
          />
        </div>

        {/* Impact tree visualization */}
        <Card className="bg-card/30 border-border">
          <CardContent className="p-4">
            <div className="flex items-center justify-between mb-3">
              <div className="flex items-center gap-2">
                <GitBranch className="w-4 h-4 text-primary" />
                <span className="font-mono text-xs uppercase tracking-wider text-muted-foreground">impact tree</span>
              </div>
              <span className="text-[11px] font-mono text-muted-foreground">
                {directCallers.length} direct · {transitiveCallers.length} transitive
              </span>
            </div>

            {/* Root node */}
            <div className="flex flex-col items-center gap-4">
              <RootNode name={blast.root.service} ns={blast.root.ns} errorRate={blast.root.errorRate} calls={blast.root.calls} />

              {blast.impacted.length > 0 ? (
                <>
                  <div className="flex flex-col items-center gap-1 text-muted-foreground">
                    <ArrowDown className="w-4 h-4" />
                    <span className="text-[10px] font-mono uppercase tracking-wider">impact propagates upstream</span>
                  </div>

                  {/* Impacted services grid */}
                  <div className="w-full grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2">
                    {blast.impacted.map(imp => (
                      <ImpactNode key={imp.service} {...imp} />
                    ))}
                  </div>
                </>
              ) : (
                <div className="text-center py-6">
                  <div className="text-sm text-emerald-300 font-mono">
                    ✓ No upstream services depend on {blast.root.service}
                  </div>
                  <p className="text-xs text-muted-foreground mt-1">
                    This is a leaf service — its failure has no blast radius.
                  </p>
                </div>
              )}
            </div>
          </CardContent>
        </Card>

        {/* Detailed impact list */}
        {blast.impacted.length > 0 && (
          <Card className="bg-card/30 border-border">
            <CardContent className="p-4">
              <div className="flex items-center gap-2 mb-3">
                <AlertOctagon className="w-4 h-4 text-rose-400" />
                <span className="font-mono text-xs uppercase tracking-wider text-muted-foreground">dependency chain</span>
              </div>
              <div className="space-y-1.5">
                {blast.impacted.slice(0, 12).map(imp => (
                  <div key={imp.service} className="flex items-center gap-2 text-xs font-mono px-3 py-2 rounded border border-border bg-muted/20">
                    <span className="text-amber-300 w-32 truncate">{imp.service}</span>
                    <span className="text-muted-foreground text-[10px] w-20 truncate">{imp.ns}</span>
                    <div className="flex-1 flex items-center gap-1 overflow-hidden">
                      {imp.path.map((p, i) => (
                        <span key={i} className="flex items-center gap-1">
                          <span className={`truncate ${i === 0 ? 'text-rose-300' : i === imp.path.length - 1 ? 'text-primary' : 'text-muted-foreground'}`}>
                            {p}
                          </span>
                          {i < imp.path.length - 1 && <span className="text-muted-foreground/50">→</span>}
                        </span>
                      ))}
                    </div>
                    <span className="text-muted-foreground text-[10px]">{imp.calls} calls</span>
                    <span className={`text-[10px] ${imp.errorRate > 0.1 ? 'text-rose-300' : imp.errorRate > 0 ? 'text-amber-300' : 'text-emerald-300'}`}>
                      {(imp.errorRate * 100).toFixed(1)}%
                    </span>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>
        )}
      </div>
    </div>
  )
}

function MetricCard({ label, value, icon, tone }: { label: string; value: string; icon: React.ReactNode; tone: 'rose' | 'amber' | 'emerald' | 'primary' }) {
  const toneCls = {
    rose: 'border-rose-500/40 text-rose-300 bg-rose-500/10',
    amber: 'border-amber-500/40 text-amber-300 bg-amber-500/10',
    emerald: 'border-emerald-500/40 text-emerald-300 bg-emerald-500/10',
    primary: 'border-primary/40 text-primary bg-primary/10',
  }[tone]
  return (
    <Card className="bg-card/30 border-border">
      <CardContent className="p-3">
        <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-muted-foreground font-mono mb-1">
          {icon}{label}
        </div>
        <div className={`font-mono text-xl font-bold ${toneCls.split(' ').find(c => c.startsWith('text-'))}`}>
          {value}
        </div>
      </CardContent>
    </Card>
  )
}

function RootNode({ name, ns, errorRate, calls }: { name: string; ns: string; errorRate: number; calls: number }) {
  const tone = errorRate > 0.1 ? 'rose' : errorRate > 0.03 ? 'amber' : 'emerald'
  const ring = {
    rose: 'border-rose-500/60 bg-rose-500/10 shadow-rose-500/20',
    amber: 'border-amber-500/60 bg-amber-500/10 shadow-amber-500/20',
    emerald: 'border-emerald-500/60 bg-emerald-500/10 shadow-emerald-500/20',
  }[tone]
  const txt = {
    rose: 'text-rose-300',
    amber: 'text-amber-300',
    emerald: 'text-emerald-300',
  }[tone]
  return (
    <div className={`relative px-6 py-3 rounded-lg border-2 ${ring} shadow-lg text-center`}>
      <div className="absolute -top-2 left-1/2 -translate-x-1/2 px-2 py-0.5 rounded-full bg-card border border-border text-[9px] font-mono uppercase tracking-wider text-muted-foreground">
        failing
      </div>
      <div className="font-mono font-semibold text-sm">{name}</div>
      <div className="text-[10px] text-muted-foreground font-mono mt-0.5">{ns}</div>
      <div className="flex items-center gap-3 justify-center mt-2 text-[10px] font-mono">
        <span className={txt}>{(errorRate * 100).toFixed(1)}% errors</span>
        <span className="text-muted-foreground">·</span>
        <span className="text-muted-foreground">{calls} calls</span>
      </div>
    </div>
  )
}

function ImpactNode({ service, ns, path, errorRate, calls }: {
  service: string; ns: string; path: string[]; errorRate: number; calls: number
}) {
  const tone = errorRate > 0.1 ? 'rose' : errorRate > 0 ? 'amber' : 'emerald'
  const border = {
    rose: 'border-rose-500/40',
    amber: 'border-amber-500/40',
    emerald: 'border-emerald-500/40',
  }[tone]
  const dot = {
    rose: 'bg-rose-400',
    amber: 'bg-amber-400',
    emerald: 'bg-emerald-400',
  }[tone]
  return (
    <div className={`px-3 py-2 rounded border ${border} bg-muted/20`}>
      <div className="flex items-center gap-1.5">
        <span className={`w-1.5 h-1.5 rounded-full ${dot} pulse-ring`} />
        <span className="font-mono text-xs font-semibold truncate">{service}</span>
      </div>
      <div className="text-[10px] text-muted-foreground font-mono mt-0.5 truncate">{ns}</div>
      <div className="flex items-center gap-2 mt-1 text-[10px] font-mono">
        <span className="text-muted-foreground">{calls} calls</span>
        <span className="text-muted-foreground">·</span>
        <span className={tone === 'rose' ? 'text-rose-300' : tone === 'amber' ? 'text-amber-300' : 'text-emerald-300'}>
          {(errorRate * 100).toFixed(1)}% err
        </span>
        <span className="text-muted-foreground">·</span>
        <span className="text-muted-foreground">depth {path.length - 1}</span>
      </div>
    </div>
  )
}
