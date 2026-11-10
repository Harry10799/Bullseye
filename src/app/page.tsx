'use client'

import { useEffect, useMemo, useState } from 'react'
import { Activity, Network, Globe2, Clock3, Zap, Flame, LayoutDashboard, ChevronRight, Github, BookOpen, Radio } from 'lucide-react'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { useTrafficStore } from '@/store/traffic-store'
import { Header } from '@/components/bullseye/header'
import { OverviewTab } from '@/components/bullseye/overview-tab'
import { LiveFeedTab } from '@/components/bullseye/live-feed-tab'
import { ServiceMapTab } from '@/components/bullseye/service-map-tab'
import { DnsTab } from '@/components/bullseye/dns-tab'
import { TimeMachineTab } from '@/components/bullseye/time-machine-tab'
import { BlastRadiusTab } from '@/components/bullseye/blast-radius-tab'
import { RetryStormsTab } from '@/components/bullseye/retry-storms-tab'
import { detectRetryStorms, isHttpError } from '@/lib/stats'

const navigation = [
  { id: 'overview', label: 'Overview', icon: LayoutDashboard, section: 'Workspace' },
  { id: 'map', label: 'Service topology', icon: Network, section: 'Observe' },
  { id: 'feed', label: 'Live traffic', icon: Activity, section: 'Observe' },
  { id: 'dns', label: 'DNS intelligence', icon: Globe2, section: 'Observe' },
  { id: 'blast', label: 'Blast radius', icon: Zap, section: 'Investigate' },
  { id: 'storms', label: 'Retry patterns', icon: Flame, section: 'Investigate' },
  { id: 'time', label: 'Time Machine', icon: Clock3, section: 'Investigate' },
]

export default function Page() {
  const connect = useTrafficStore(s => s.connect)
  const connected = useTrafficStore(s => s.connected)
  const entries = useTrafficStore(s => s.entries)
  const [tab, setTab] = useState('overview')

  useEffect(() => { connect() }, [connect])

  const errorCount = useMemo(() => entries.filter(isHttpError).length, [entries])
  const stormCount = useMemo(() => detectRetryStorms(entries).length, [entries])

  const badges: Record<string, number> = { blast: errorCount, storms: stormCount }

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-background">
      <Header />
      <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1">
        <aside className="hidden w-[228px] shrink-0 flex-col border-r border-border/80 bg-card/35 lg:flex">
          <div className="px-4 pb-3 pt-5">
            <div className="text-[10px] font-semibold uppercase tracking-[0.18em] text-muted-foreground">Workspace</div>
            <div className="mt-1 text-xs text-muted-foreground">Network intelligence</div>
          </div>
          <TabsList className="flex h-auto flex-1 flex-col items-stretch justify-start gap-1 rounded-none bg-transparent px-2 py-0">
            {['Workspace', 'Observe', 'Investigate'].map(section => (
              <div key={section} className="mt-3">
                {section !== 'Workspace' && <div className="mb-2 px-3 text-[9px] font-semibold uppercase tracking-[0.16em] text-muted-foreground/65">{section}</div>}
                <div className="space-y-1">
                  {navigation.filter(item => item.section === section).map(item => {
                    const Icon = item.icon
                    const active = tab === item.id
                    return (
                      <TabsTrigger key={item.id} value={item.id} className="group flex h-10 w-full justify-start gap-3 rounded-lg border border-transparent px-3 text-left text-xs text-muted-foreground shadow-none transition data-[state=active]:border-teal-400/15 data-[state=active]:bg-teal-400/[0.09] data-[state=active]:text-teal-200 data-[state=active]:shadow-none">
                        <Icon className={`h-4 w-4 shrink-0 ${active ? 'text-teal-300' : 'text-muted-foreground group-hover:text-foreground'}`} />
                        <span className="flex-1">{item.label}</span>
                        {badges[item.id] > 0 && <Badge variant="outline" className="h-5 min-w-5 justify-center border-rose-400/20 bg-rose-400/10 px-1 text-[9px] text-rose-300">{badges[item.id] > 99 ? '99+' : badges[item.id]}</Badge>}
                        {active && <ChevronRight className="h-3 w-3 text-teal-300" />}
                      </TabsTrigger>
                    )
                  })}
                </div>
              </div>
            ))}
          </TabsList>
          <div className="m-3 rounded-xl border border-border/70 bg-background/50 p-3">
            <div className="flex items-center gap-2 text-xs font-medium"><span className={`h-2 w-2 rounded-full ${connected ? 'bg-emerald-400 pulse-ring' : 'bg-rose-400'}`} /> Capture pipeline</div>
            <div className="mt-1 text-[10px] text-muted-foreground">{connected ? 'Connected · receiving events' : 'Disconnected · waiting for hub'}</div>
            <div className="mt-3 flex items-center justify-between text-[10px] text-muted-foreground"><span>Buffered events</span><span className="font-mono tabular-nums">{entries.length.toLocaleString()} / 1,500</span></div>
            <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-teal-400 transition-all" style={{ width: `${Math.min(100, entries.length / 15)}%` }} /></div>
          </div>
          <div className="border-t border-border/70 px-4 py-3">
            <div className="flex items-center gap-2 text-[10px] text-muted-foreground"><BookOpen className="h-3 w-3" /> Documentation <span className="ml-auto">↗</span></div>
            <div className="mt-2 flex items-center gap-2 text-[10px] text-muted-foreground"><Github className="h-3 w-3" /> GitHub <span className="ml-auto">↗</span></div>
          </div>
        </aside>

        <main className="flex min-w-0 flex-1 flex-col">
          <div className="flex h-12 shrink-0 items-center justify-between border-b border-border/70 bg-background/70 px-4 lg:px-6">
            <div className="flex items-center gap-2 text-xs text-muted-foreground"><span className="text-foreground/80">Bullseye</span><ChevronRight className="h-3 w-3" /><span>{navigation.find(item => item.id === tab)?.label || 'Overview'}</span></div>
            <div className="flex items-center gap-2 text-[10px] font-mono text-muted-foreground"><Radio className={`h-3 w-3 ${connected ? 'text-emerald-400' : 'text-rose-400'}`} /> {connected ? 'STREAM ACTIVE' : 'STREAM OFFLINE'} <span className="text-border">|</span> <span>{entries.length.toLocaleString()} EVENTS</span></div>
          </div>

          <div className="min-h-0 flex-1">
            <TabsContent value="overview" className="m-0 h-full data-[state=inactive]:hidden"><OverviewTab onNavigate={setTab} /></TabsContent>
            <TabsContent value="feed" className="m-0 h-full data-[state=inactive]:hidden"><LiveFeedTab /></TabsContent>
            <TabsContent value="map" className="m-0 h-full data-[state=inactive]:hidden"><ServiceMapTab /></TabsContent>
            <TabsContent value="dns" className="m-0 h-full data-[state=inactive]:hidden"><DnsTab /></TabsContent>
            <TabsContent value="time" className="m-0 h-full data-[state=inactive]:hidden"><TimeMachineTab /></TabsContent>
            <TabsContent value="blast" className="m-0 h-full data-[state=inactive]:hidden"><BlastRadiusTab /></TabsContent>
            <TabsContent value="storms" className="m-0 h-full data-[state=inactive]:hidden"><RetryStormsTab /></TabsContent>
          </div>
        </main>
      </Tabs>
    </div>
  )
}
