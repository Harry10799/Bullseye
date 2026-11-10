'use client'

import { useMemo } from 'react'
import { Activity, AlertTriangle, ArrowDownRight, ArrowUpRight, Boxes, Globe2, Network, Radio, Zap } from 'lucide-react'
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis } from 'recharts'
import { Badge } from '@/components/ui/badge'
import { useTrafficStore } from '@/store/traffic-store'
import { computeServiceStats, detectRetryStorms, isHttpError } from '@/lib/stats'
import { fmtTime } from '@/lib/ui'

function MetricCard({ label, value, detail, icon: Icon, tone = 'teal' }: {
  label: string; value: string | number; detail: string; icon: typeof Activity; tone?: 'teal' | 'rose' | 'amber' | 'blue'
}) {
  const tones = {
    teal: 'text-teal-300 bg-teal-400/10 border-teal-400/15',
    rose: 'text-rose-300 bg-rose-400/10 border-rose-400/15',
    amber: 'text-amber-300 bg-amber-400/10 border-amber-400/15',
    blue: 'text-sky-300 bg-sky-400/10 border-sky-400/15',
  }
  return (
    <div className="rounded-xl border border-border/80 bg-card/70 p-4 shadow-sm">
      <div className="flex items-center justify-between">
        <span className="text-[11px] font-medium uppercase tracking-[0.14em] text-muted-foreground">{label}</span>
        <span className={`grid h-8 w-8 place-items-center rounded-lg border ${tones[tone]}`}><Icon className="h-4 w-4" /></span>
      </div>
      <div className="mt-3 text-2xl font-semibold tracking-tight tabular-nums">{value}</div>
      <div className="mt-1 text-xs text-muted-foreground">{detail}</div>
    </div>
  )
}

export function OverviewTab({ onNavigate }: { onNavigate: (tab: string) => void }) {
  const entries = useTrafficStore(s => s.entries)
  const connected = useTrafficStore(s => s.connected)
  const topology = useTrafficStore(s => s.topology)
  const errors = useMemo(() => entries.filter(isHttpError), [entries])
  const services = useMemo(() => computeServiceStats(entries, topology || undefined), [entries, topology])
  const storms = useMemo(() => detectRetryStorms(entries), [entries])
  const dns = useMemo(() => entries.filter(e => e.protocol === 'DNS'), [entries])
  const chartData = useMemo(() => {
    const now = Date.now()
    return Array.from({ length: 12 }, (_, i) => {
      const start = now - (11 - i) * 60_000
      const end = start + 60_000
      const count = entries.filter(e => e.timestamp >= start && e.timestamp < end).length
      return { minute: new Date(start).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }), events: count }
    })
  }, [entries])
  const recent = entries.slice(0, 6)
  const topServices = services.slice(0, 5)
  const errorRate = entries.length ? (errors.length / entries.length) * 100 : 0

  return (
    <div className="h-full overflow-y-auto scrollbar-thin p-4 lg:p-6 space-y-5">
      <section className="relative overflow-hidden rounded-2xl border border-teal-400/20 bg-gradient-to-br from-teal-400/[0.12] via-card to-card p-5 lg:p-6">
        <div className="pointer-events-none absolute -right-16 -top-24 h-64 w-64 rounded-full bg-teal-400/10 blur-3xl" />
        <div className="relative flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
          <div>
            <div className="flex items-center gap-2 text-xs font-medium uppercase tracking-[0.18em] text-teal-300"><span className={`h-2 w-2 rounded-full ${connected ? 'bg-emerald-400 animate-pulse' : 'bg-rose-400'}`} /> Network operations</div>
            <h1 className="mt-3 text-2xl font-semibold tracking-tight lg:text-3xl">See what your services are saying.</h1>
            <p className="mt-2 max-w-2xl text-sm text-muted-foreground">Live network intelligence across observed service-to-service traffic. Start with the topology, then drill into the events behind an issue.</p>
          </div>
          <button onClick={() => onNavigate('map')} className="inline-flex h-10 shrink-0 items-center justify-center gap-2 rounded-lg bg-teal-300 px-4 text-sm font-semibold text-slate-950 transition hover:bg-teal-200">
            <Network className="h-4 w-4" /> Explore topology
          </button>
        </div>
      </section>

      <section className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard label="Captured events" value={entries.length.toLocaleString()} detail="Current in-memory buffer" icon={Activity} tone="teal" />
        <MetricCard label="Observed services" value={services.length || topology?.services.length || 0} detail="Services seen in traffic / topology" icon={Boxes} tone="blue" />
        <MetricCard label="Error events" value={errors.length.toLocaleString()} detail={`${errorRate.toFixed(1)}% of buffered events`} icon={AlertTriangle} tone="rose" />
        <MetricCard label="DNS events" value={dns.length.toLocaleString()} detail={`${storms.length} retry patterns detected`} icon={Globe2} tone="amber" />
      </section>

      <section className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1.65fr)_minmax(300px,1fr)]">
        <div className="rounded-xl border border-border/80 bg-card/70 p-4">
          <div className="flex items-start justify-between gap-3">
            <div><h2 className="text-sm font-semibold">Traffic activity</h2><p className="mt-1 text-xs text-muted-foreground">Events per minute · last 12 minutes</p></div>
            <Badge variant="outline" className="gap-1.5 border-border bg-background/50 text-[10px]"><Radio className="h-3 w-3 text-teal-300" /> {connected ? 'STREAM CONNECTED' : 'STREAM OFFLINE'}</Badge>
          </div>
          <div className="mt-4 h-[220px] w-full">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={chartData} margin={{ top: 8, right: 8, left: -24, bottom: 0 }}>
                <defs><linearGradient id="trafficFill" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="#2dd4bf" stopOpacity={0.32} /><stop offset="95%" stopColor="#2dd4bf" stopOpacity={0.01} /></linearGradient></defs>
                <XAxis dataKey="minute" tick={{ fill: '#7c8998', fontSize: 10 }} axisLine={false} tickLine={false} minTickGap={24} />
                <Tooltip contentStyle={{ background: '#101a24', border: '1px solid #2b3a48', borderRadius: 10, color: '#e5edf5', fontSize: 12 }} />
                <Area type="monotone" dataKey="events" stroke="#2dd4bf" strokeWidth={2} fill="url(#trafficFill)" />
              </AreaChart>
            </ResponsiveContainer>
          </div>
          <p className="text-[11px] text-muted-foreground">Chart is derived from events currently held in the browser buffer; it is not a durable historical metric.</p>
        </div>

        <div className="rounded-xl border border-border/80 bg-card/70 p-4">
          <div className="flex items-center justify-between"><div><h2 className="text-sm font-semibold">Service activity</h2><p className="mt-1 text-xs text-muted-foreground">Most active observed services</p></div><button onClick={() => onNavigate('map')} className="text-xs text-teal-300 hover:text-teal-200">View map ↗</button></div>
          <div className="mt-4 space-y-3">
            {topServices.length ? topServices.map((svc, i) => (
              <div key={svc.service} className="flex items-center gap-3">
                <span className="grid h-7 w-7 shrink-0 place-items-center rounded-md bg-muted/70 text-[10px] font-mono text-muted-foreground">{String(i + 1).padStart(2, '0')}</span>
                <div className="min-w-0 flex-1"><div className="truncate text-xs font-medium">{svc.service}</div><div className="truncate text-[10px] text-muted-foreground">{svc.ns}</div></div>
                <div className="text-right"><div className="text-xs font-mono tabular-nums">{(svc.incoming + svc.outgoing).toLocaleString()}</div><div className={`text-[10px] ${svc.errorRate > 0.1 ? 'text-rose-300' : 'text-muted-foreground'}`}>{(svc.errorRate * 100).toFixed(1)}% errors</div></div>
              </div>
            )) : <div className="rounded-lg border border-dashed border-border p-6 text-center text-xs text-muted-foreground">Waiting for service traffic…</div>}
          </div>
        </div>
      </section>

      <section className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <div className="rounded-xl border border-border/80 bg-card/70 p-4">
          <div className="flex items-center justify-between"><div><h2 className="text-sm font-semibold">Recent network events</h2><p className="mt-1 text-xs text-muted-foreground">Latest captured requests and responses</p></div><button onClick={() => onNavigate('feed')} className="text-xs text-teal-300 hover:text-teal-200">Open live feed ↗</button></div>
          <div className="mt-3 divide-y divide-border/60">
            {recent.length ? recent.map(e => (
              <div key={e.id} className="flex items-center gap-3 py-2.5">
                <span className={`h-2 w-2 shrink-0 rounded-full ${isHttpError(e) ? 'bg-rose-400' : 'bg-emerald-400'}`} />
                <span className="w-12 shrink-0 text-[10px] font-mono text-muted-foreground">{e.protocol}</span>
                <span className="min-w-0 flex-1 truncate text-xs">{e.source.service} <span className="text-muted-foreground">→</span> {e.destination.service}</span>
                <span className="shrink-0 text-[10px] font-mono text-muted-foreground">{e.latencyMs}ms</span>
                <span className="hidden shrink-0 text-[10px] text-muted-foreground sm:block">{fmtTime(e.timestamp)}</span>
              </div>
            )) : <div className="py-8 text-center text-xs text-muted-foreground">No events captured yet. Start the sniffer or simulator to populate the dashboard.</div>}
          </div>
        </div>
        <div className="rounded-xl border border-border/80 bg-card/70 p-4">
          <div className="flex items-center justify-between"><div><h2 className="text-sm font-semibold">Investigation shortcuts</h2><p className="mt-1 text-xs text-muted-foreground">Move from signal to context</p></div><Zap className="h-4 w-4 text-amber-300" /></div>
          <div className="mt-4 grid grid-cols-1 gap-2 sm:grid-cols-2">
            {[
              { id: 'blast', title: 'Blast radius', desc: 'Trace observed callers of a failing service', icon: ArrowDownRight, tone: 'text-rose-300 bg-rose-400/10' },
              { id: 'storms', title: 'Retry patterns', desc: 'Inspect repeated request clusters', icon: ArrowUpRight, tone: 'text-amber-300 bg-amber-400/10' },
              { id: 'dns', title: 'DNS intelligence', desc: 'Review queries and response codes', icon: Globe2, tone: 'text-sky-300 bg-sky-400/10' },
              { id: 'time', title: 'Time Machine', desc: 'Replay the current browser buffer', icon: Activity, tone: 'text-teal-300 bg-teal-400/10' },
            ].map(item => {
              const Icon = item.icon
              return <button key={item.id} onClick={() => onNavigate(item.id)} className="flex items-start gap-3 rounded-lg border border-border/70 bg-background/40 p-3 text-left transition hover:border-teal-400/30 hover:bg-teal-400/[0.04]"><span className={`grid h-8 w-8 shrink-0 place-items-center rounded-lg ${item.tone}`}><Icon className="h-4 w-4" /></span><span><span className="block text-xs font-medium">{item.title}</span><span className="mt-1 block text-[10px] leading-relaxed text-muted-foreground">{item.desc}</span></span></button>
            })}
          </div>
        </div>
      </section>
    </div>
  )
}
