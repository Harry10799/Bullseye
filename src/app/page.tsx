'use client'

import { useEffect, useState } from 'react'
import { Activity, Globe, Network, Clock, Zap, Flame, Github, Book } from 'lucide-react'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { useTrafficStore } from '@/store/traffic-store'
import { Header } from '@/components/kubeshark/header'
import { LiveFeedTab } from '@/components/kubeshark/live-feed-tab'
import { ServiceMapTab } from '@/components/kubeshark/service-map-tab'
import { DnsTab } from '@/components/kubeshark/dns-tab'
import { TimeMachineTab } from '@/components/kubeshark/time-machine-tab'
import { BlastRadiusTab } from '@/components/kubeshark/blast-radius-tab'
import { RetryStormsTab } from '@/components/kubeshark/retry-storms-tab'
import { detectRetryStorms, isHttpError } from '@/lib/stats'

export default function Page() {
  const connect = useTrafficStore(s => s.connect)
  const connected = useTrafficStore(s => s.connected)
  const entries = useTrafficStore(s => s.entries)
  const [tab, setTab] = useState('feed')

  useEffect(() => { connect() }, [connect])

  // derived counts for tab badges
  const errorCount = entries.filter(isHttpError).length
  const dnsCount = entries.filter(e => e.protocol === 'DNS').length
  const stormCount = detectRetryStorms(entries).length

  return (
    <div className="h-screen flex flex-col bg-background overflow-hidden">
      <Header />

      <Tabs value={tab} onValueChange={setTab} className="flex-1 flex flex-col min-h-0">
        <div className="border-b border-border bg-card/20 px-3 flex items-center gap-2">
          <TabsList className="bg-transparent h-12 p-0 gap-1">
            <TabsTrigger value="feed" className="gap-1.5 data-[state=active]:bg-primary/10 data-[state=active]:text-primary h-9 px-3 text-xs font-mono">
              <Activity className="w-3.5 h-3.5" />
              live feed
            </TabsTrigger>
            <TabsTrigger value="map" className="gap-1.5 data-[state=active]:bg-primary/10 data-[state=active]:text-primary h-9 px-3 text-xs font-mono">
              <Network className="w-3.5 h-3.5" />
              service map
            </TabsTrigger>
            <TabsTrigger value="dns" className="gap-1.5 data-[state=active]:bg-primary/10 data-[state=active]:text-primary h-9 px-3 text-xs font-mono">
              <Globe className="w-3.5 h-3.5" />
              dns
              {dnsCount > 0 && (
                <Badge variant="outline" className="ml-1 h-4 px-1 text-[9px] font-mono border-border">
                  {dnsCount > 999 ? '999+' : dnsCount}
                </Badge>
              )}
            </TabsTrigger>
            <TabsTrigger value="time" className="gap-1.5 data-[state=active]:bg-primary/10 data-[state=active]:text-primary h-9 px-3 text-xs font-mono">
              <Clock className="w-3.5 h-3.5" />
              time machine
            </TabsTrigger>
            <TabsTrigger value="blast" className="gap-1.5 data-[state=active]:bg-primary/10 data-[state=active]:text-primary h-9 px-3 text-xs font-mono">
              <Zap className="w-3.5 h-3.5" />
              blast radius
              {errorCount > 0 && (
                <Badge variant="outline" className="ml-1 h-4 px-1 text-[9px] font-mono border-rose-500/40 text-rose-300 bg-rose-500/10">
                  {errorCount > 999 ? '999+' : errorCount}
                </Badge>
              )}
            </TabsTrigger>
            <TabsTrigger value="storms" className="gap-1.5 data-[state=active]:bg-primary/10 data-[state=active]:text-primary h-9 px-3 text-xs font-mono">
              <Flame className="w-3.5 h-3.5" />
              retry storms
              {stormCount > 0 && (
                <Badge variant="outline" className="ml-1 h-4 px-1 text-[9px] font-mono border-amber-500/40 text-amber-300 bg-amber-500/10">
                  {stormCount}
                </Badge>
              )}
            </TabsTrigger>
          </TabsList>

          {/* Right side: status + sim badge */}
          <div className="flex-1" />
          <div className="hidden md:flex items-center gap-2 text-[11px] font-mono text-muted-foreground pr-1">
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="inline-flex items-center gap-1">
                    <span className={`w-1.5 h-1.5 rounded-full ${connected ? 'bg-emerald-400 pulse-ring' : 'bg-rose-400'}`} />
                    {connected ? 'sniffer connected' : 'sniffer offline'}
                  </span>
                </TooltipTrigger>
                <TooltipContent side="bottom">
                  <p className="font-mono text-xs">eBPF / AF_PACKET DaemonSet on each node</p>
                  <p className="text-muted-foreground text-xs">Streaming L7 packets via socket.io on port 3030</p>
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
            <span className="text-muted-foreground/40">·</span>
            <span className="inline-flex items-center gap-1">
              <Book className="w-3 h-3" />
              docs
            </span>
            <span className="text-muted-foreground/40">·</span>
            <span className="inline-flex items-center gap-1">
              <Github className="w-3 h-3" />
              github
            </span>
          </div>
        </div>

        <TabsContent value="feed" className="flex-1 m-0 min-h-0 data-[state=inactive]:hidden">
          <LiveFeedTab />
        </TabsContent>
        <TabsContent value="map" className="flex-1 m-0 min-h-0 data-[state=inactive]:hidden">
          <ServiceMapTab />
        </TabsContent>
        <TabsContent value="dns" className="flex-1 m-0 min-h-0 data-[state=inactive]:hidden">
          <DnsTab />
        </TabsContent>
        <TabsContent value="time" className="flex-1 m-0 min-h-0 data-[state=inactive]:hidden">
          <TimeMachineTab />
        </TabsContent>
        <TabsContent value="blast" className="flex-1 m-0 min-h-0 data-[state=inactive]:hidden">
          <BlastRadiusTab />
        </TabsContent>
        <TabsContent value="storms" className="flex-1 m-0 min-h-0 data-[state=inactive]:hidden">
          <RetryStormsTab />
        </TabsContent>
      </Tabs>
    </div>
  )
}
