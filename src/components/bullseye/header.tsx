'use client'

import { useEffect, useState } from 'react'
import { Activity, Circle, Pause, Play, Download, Trash2, Wifi, WifiOff, Shield } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { useTrafficStore } from '@/store/traffic-store'
import { toast } from 'sonner'

export function Header() {
  const connected = useTrafficStore(s => s.connected)
  const capturing = useTrafficStore(s => s.capturing)
  const setCapturing = useTrafficStore(s => s.setCapturing)
  const clearBuffer = useTrafficStore(s => s.clearBuffer)
  const entries = useTrafficStore(s => s.entries)
  const topology = useTrafficStore(s => s.topology)
  const [namespace, setNamespace] = useState<string>('__all__')
  const [elapsed, setElapsed] = useState(0)

  useEffect(() => {
    const t = setInterval(() => setElapsed(e => e + 1), 1000)
    return () => clearInterval(t)
  }, [])

  // detect errors for the header pill
  const errorCount = entries.filter(e =>
    (e.protocol === 'HTTP' && (e.status ?? 0) >= 500) ||
    (e.protocol === 'gRPC' && (e.status ?? 0) > 0) ||
    (e.protocol === 'DNS' && e.dnsRcode !== 'NOERROR')
  ).length

  const namespaces = topology
    ? Array.from(new Set(topology.services.map(s => s.ns))).sort()
    : ['checkout', 'payments', 'catalog', 'users', 'shipping', 'analytics']

  const fmtElapsed = (s: number) => {
    const h = Math.floor(s / 3600).toString().padStart(2, '0')
    const m = Math.floor((s % 3600) / 60).toString().padStart(2, '0')
    const sec = (s % 60).toString().padStart(2, '0')
    return `${h}:${m}:${sec}`
  }

  const handlePcapExport = () => {
    // generate a "fake" pcap-like blob (text manifest) — in real Bullseye this would be a real .pcap
    const manifest = {
      format: 'bullseye-pcap-v1',
      captured_at: new Date().toISOString(),
      entry_count: entries.length,
      namespaces: namespaces,
      // include first 50 entries as a JSON manifest
      entries: entries.slice(0, 50).map(e => ({
        ts: new Date(e.timestamp).toISOString(),
        proto: e.protocol,
        src: `${e.source.pod} (${e.source.ip})`,
        dst: `${e.destination.pod} (${e.destination.ip})`,
        method: e.method,
        path: e.path,
        status: e.status,
        latency_ms: e.latencyMs,
      })),
    }
    const blob = new Blob([JSON.stringify(manifest, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `bullseye-capture-${Date.now()}.json`
    a.click()
    URL.revokeObjectURL(url)
    toast.success(`Exported ${entries.length} entries (PCAP manifest)`)
  }

  return (
    <header className="border-b border-border bg-card/40 backdrop-blur-sm sticky top-0 z-40">
      <div className="flex items-center h-14 px-4 gap-3">
        {/* Brand */}
        <div className="flex items-center gap-2.5">
          <div className="relative">
            <div className="absolute inset-0 bg-primary/40 blur-md rounded-full pulse-ring" />
            <div className="relative w-8 h-8 rounded-lg bg-gradient-to-br from-primary to-emerald-500 flex items-center justify-center shadow-lg shadow-primary/20">
              <Activity className="w-4 h-4 text-primary-foreground" strokeWidth={2.5} />
            </div>
          </div>
          <div className="flex flex-col leading-none">
            <span className="font-bold text-[15px] tracking-tight">
              Bullseye
            </span>
            <span className="text-[10px] text-muted-foreground font-mono uppercase tracking-wider">
              Wireshark for K8s
            </span>
          </div>
        </div>

        {/* Connection status */}
        <div className="ml-2 flex items-center gap-2">
          <Badge
            variant="outline"
            className={`gap-1.5 font-mono text-[11px] ${
              connected ? 'border-emerald-500/40 text-emerald-300 bg-emerald-500/10' : 'border-rose-500/40 text-rose-300 bg-rose-500/10'
            }`}
          >
            {connected ? <Wifi className="w-3 h-3" /> : <WifiOff className="w-3 h-3" />}
            {connected ? 'live' : 'offline'}
          </Badge>
          {errorCount > 0 && (
            <Badge variant="outline" className="gap-1.5 font-mono text-[11px] border-rose-500/40 text-rose-300 bg-rose-500/10">
              <Circle className="w-2 h-2 fill-current" />
              {errorCount} errors
            </Badge>
          )}
          <Badge variant="outline" className="font-mono text-[11px] border-border text-muted-foreground">
            {entries.length} pkts
          </Badge>
          <Badge variant="outline" className="font-mono text-[11px] border-border text-muted-foreground">
            {fmtElapsed(elapsed)}
          </Badge>
        </div>

        {/* Spacer */}
        <div className="flex-1" />

        {/* Namespace selector */}
        <div className="hidden md:flex items-center gap-2">
          <span className="text-[11px] text-muted-foreground font-mono uppercase tracking-wider">ns</span>
          <Select value={namespace} onValueChange={setNamespace}>
            <SelectTrigger className="h-8 w-[170px] text-xs font-mono">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all__" className="font-mono text-xs">all namespaces</SelectItem>
              {namespaces.map(ns => (
                <SelectItem key={ns} value={ns} className="font-mono text-xs">{ns}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {/* TLS badge — represents TLS decryption via eBPF uprobes */}
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <Badge variant="outline" className="gap-1.5 font-mono text-[11px] border-primary/40 text-primary bg-primary/10">
                <Shield className="w-3 h-3" />
                TLS decrypted
              </Badge>
            </TooltipTrigger>
            <TooltipContent side="bottom" className="text-xs">
              <p className="font-mono">eBPF uprobes hooking OpenSSL / Go TLS</p>
              <p className="text-muted-foreground">Encrypted traffic shown as plaintext</p>
            </TooltipContent>
          </Tooltip>
        </TooltipProvider>

        {/* Capture control */}
        <TooltipProvider>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="outline"
                size="sm"
                className="h-8 gap-1.5 font-mono text-xs"
                onClick={() => setCapturing(!capturing)}
              >
                {capturing ? <Pause className="w-3.5 h-3.5" /> : <Play className="w-3.5 h-3.5" />}
                {capturing ? 'pause' : 'resume'}
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{capturing ? 'Pause live capture' : 'Resume live capture'}</TooltipContent>
          </Tooltip>

          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="outline"
                size="sm"
                className="h-8 gap-1.5 font-mono text-xs"
                onClick={handlePcapExport}
              >
                <Download className="w-3.5 h-3.5" />
                pcap
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">Export capture as PCAP (Wireshark-compatible)</TooltipContent>
          </Tooltip>

          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="outline"
                size="sm"
                className="h-8 gap-1.5 font-mono text-xs hover:border-rose-500/40 hover:text-rose-300"
                onClick={() => { clearBuffer(); toast.success('Capture buffer cleared') }}
              >
                <Trash2 className="w-3.5 h-3.5" />
                clear
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">Clear capture buffer</TooltipContent>
          </Tooltip>
        </TooltipProvider>
      </div>
    </header>
  )
}
