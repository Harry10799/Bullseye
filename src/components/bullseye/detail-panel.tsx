'use client'

import { useMemo, useState } from 'react'
import { X, Clock, ArrowRight, Lock, Hash, RotateCw, Download, Copy } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ScrollArea } from '@/components/ui/scroll-area'
import { useTrafficStore, getEntryById } from '@/store/traffic-store'
import type { TrafficEntry } from '@/lib/traffic-types'
import {
  statusColor, protocolBadge, latencyColor, fmtTime, fmtBytes, prettyBody, highlightJson,
} from '@/lib/ui'
import { toast } from 'sonner'

export function DetailPanel({ entryId }: { entryId: string | null }) {
  const state = useTrafficStore()
  const entry = entryId ? getEntryById(state, entryId) : undefined
  const [tab, setTab] = useState('overview')

  if (!entry) {
    return (
      <div className="h-full flex items-center justify-center text-center px-6">
        <div className="space-y-2">
          <div className="w-12 h-12 mx-auto rounded-full bg-muted/50 flex items-center justify-center">
            <ArrowRight className="w-5 h-5 text-muted-foreground" />
          </div>
          <p className="text-sm text-muted-foreground">Select a packet to inspect</p>
          <p className="text-xs text-muted-foreground/70">Click any row in the live feed to view full request / response payloads</p>
        </div>
      </div>
    )
  }

  return <DetailContent entry={entry} onClose={() => state.select(null)} tab={tab} setTab={setTab} />
}

function DetailContent({ entry, onClose, tab, setTab }: {
  entry: TrafficEntry
  onClose: () => void
  tab: string
  setTab: (s: string) => void
}) {
  const proto = protocolBadge(entry.protocol)
  const reqBody = useMemo(() => prettyBody(entry.request.body ?? undefined), [entry])
  const respBody = useMemo(() => prettyBody(entry.response.body ?? undefined), [entry])

  return (
    <div className="h-full flex flex-col bg-card/30">
      {/* Header strip */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-border bg-card/50">
        <div className="flex items-center gap-2 min-w-0">
          <Badge variant="outline" className={`text-[10px] font-mono border ${proto.cls}`}>
            {proto.label}
          </Badge>
          {entry.tls && (
            <Badge variant="outline" className="text-[10px] font-mono border-primary/30 text-primary bg-primary/10 gap-1">
              <Lock className="w-2.5 h-2.5" /> TLS
            </Badge>
          )}
          {entry.isRetry && (
            <Badge variant="outline" className="text-[10px] font-mono border-amber-500/40 text-amber-300 bg-amber-500/10 gap-1">
              <RotateCw className="w-2.5 h-2.5" /> retry
            </Badge>
          )}
          <span className="font-mono text-xs text-muted-foreground truncate">{entry.id}</span>
        </div>
        <Button variant="ghost" size="sm" className="h-7 w-7 p-0" onClick={onClose}>
          <X className="w-3.5 h-3.5" />
        </Button>
      </div>

      <Tabs value={tab} onValueChange={setTab} className="flex-1 flex flex-col min-h-0">
        <TabsList className="grid grid-cols-4 h-9 rounded-none border-b border-border bg-transparent p-0">
          <TabsTrigger value="overview" className="rounded-none text-xs font-mono">overview</TabsTrigger>
          <TabsTrigger value="request" className="rounded-none text-xs font-mono">request</TabsTrigger>
          <TabsTrigger value="response" className="rounded-none text-xs font-mono">response</TabsTrigger>
          <TabsTrigger value="meta" className="rounded-none text-xs font-mono">meta</TabsTrigger>
        </TabsList>

        <ScrollArea className="flex-1 min-h-0 scrollbar-thin">
          <TabsContent value="overview" className="m-0 p-3 space-y-3 text-xs">
            <Field label="time" value={fmtTime(entry.timestamp)} mono />
            <Field label="protocol" value={entry.protocol} mono />
            {entry.method && <Field label="method" value={entry.method} mono />}
            {entry.path && <Field label="path" value={entry.path} mono highlight />}
            <Field label="status" value={entry.status !== undefined ? String(entry.status) : '—'} mono valueClass={statusColor(entry.status, entry.protocol)} />
            {entry.grpcStatus && <Field label="grpc status" value={entry.grpcStatus} mono valueClass={statusColor(entry.status, entry.protocol)} />}
            <Field label="latency" value={`${entry.latencyMs} ms`} mono valueClass={latencyColor(entry.latencyMs)} />
            <Field label="tls" value={entry.tls ? 'decrypted' : 'plaintext'} mono />

            <div className="pt-2 border-t border-border">
              <div className="text-[10px] uppercase tracking-wider text-muted-foreground mb-1.5">source</div>
              <div className="font-mono text-xs space-y-1 bg-muted/30 rounded p-2">
                <div><span className="text-muted-foreground">pod:</span> <span className="text-foreground">{entry.source.pod}</span></div>
                <div><span className="text-muted-foreground">svc:</span> <span className="text-foreground">{entry.source.service}</span></div>
                <div><span className="text-muted-foreground">ns:</span> <span className="text-foreground">{entry.source.ns}</span></div>
                <div><span className="text-muted-foreground">ip:</span> <span className="text-foreground">{entry.source.ip}:{entry.source.port}</span></div>
              </div>
            </div>

            <div className="flex items-center justify-center py-1">
              <ArrowRight className="w-4 h-4 text-primary rotate-90" />
            </div>

            <div>
              <div className="text-[10px] uppercase tracking-wider text-muted-foreground mb-1.5">destination</div>
              <div className="font-mono text-xs space-y-1 bg-muted/30 rounded p-2">
                <div><span className="text-muted-foreground">pod:</span> <span className="text-foreground">{entry.destination.pod}</span></div>
                <div><span className="text-muted-foreground">svc:</span> <span className="text-foreground">{entry.destination.service}</span></div>
                <div><span className="text-muted-foreground">ns:</span> <span className="text-foreground">{entry.destination.ns}</span></div>
                <div><span className="text-muted-foreground">ip:</span> <span className="text-foreground">{entry.destination.ip}:{entry.destination.port}</span></div>
              </div>
            </div>

            {entry.dnsQuery && (
              <div className="pt-2 border-t border-border space-y-1.5">
                <div className="text-[10px] uppercase tracking-wider text-muted-foreground">dns</div>
                <Field label="query" value={entry.dnsQuery} mono highlight />
                <Field label="type" value={entry.dnsType || '—'} mono />
                <Field label="rcode" value={entry.dnsRcode || '—'} mono valueClass={entry.dnsRcode === 'NXDOMAIN' ? 'text-rose-400' : 'text-emerald-400'} />
                {entry.dnsAnswer && entry.dnsAnswer.length > 0 && (
                  <Field label="answer" value={entry.dnsAnswer.join(', ')} mono />
                )}
                {entry.dnsTtl !== undefined && <Field label="ttl" value={`${entry.dnsTtl}s`} mono />}
              </div>
            )}

            {entry.requestId && (
              <div className="pt-2 border-t border-border">
                <Field label="request id" value={entry.requestId} mono />
              </div>
            )}
          </TabsContent>

          <TabsContent value="request" className="m-0 p-3 space-y-3">
            <HeadersBlock title="request headers" headers={entry.request.headers} />
            <BodyBlock title="request body" body={reqBody} size={entry.request.size} />
          </TabsContent>

          <TabsContent value="response" className="m-0 p-3 space-y-3">
            <HeadersBlock title="response headers" headers={entry.response.headers} />
            <BodyBlock title="response body" body={respBody} size={entry.response.size} />
          </TabsContent>

          <TabsContent value="meta" className="m-0 p-3 space-y-2 text-xs">
            <Field label="entry id" value={entry.id} mono />
            <Field label="captured at" value={new Date(entry.timestamp).toISOString()} mono />
            <Field label="source pod" value={entry.source.pod} mono />
            <Field label="destination pod" value={entry.destination.pod} mono />
            <Field label="tls" value={entry.tls ? 'decrypted via eBPF uprobe' : 'plaintext'} mono />
            <Field label="request size" value={fmtBytes(entry.request.size)} mono />
            <Field label="response size" value={fmtBytes(entry.response.size)} mono />
            <Field label="total payload" value={fmtBytes(entry.request.size + entry.response.size)} mono />
            <div className="pt-3 border-t border-border">
              <Button variant="outline" size="sm" className="w-full h-8 gap-1.5 font-mono text-xs" onClick={() => { toast.success('Packet exported to PCAP') }}>
                <Download className="w-3.5 h-3.5" />
                export this packet
              </Button>
            </div>
          </TabsContent>
        </ScrollArea>
      </Tabs>
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

function HeadersBlock({ title, headers }: { title: string; headers: Record<string, string> }) {
  const keys = Object.keys(headers)
  return (
    <div>
      <div className="flex items-center justify-between mb-1.5">
        <span className="text-[10px] uppercase tracking-wider text-muted-foreground">{title}</span>
        <Button
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-[10px] gap-1 font-mono"
          onClick={() => { navigator.clipboard.writeText(JSON.stringify(headers, null, 2)); toast.success('Headers copied') }}
        >
          <Copy className="w-3 h-3" /> copy
        </Button>
      </div>
      <div className="rounded border border-border bg-muted/20 divide-y divide-border">
        {keys.length === 0 ? (
          <div className="p-2 text-xs text-muted-foreground font-mono italic">(no headers)</div>
        ) : keys.map(k => (
          <div key={k} className="grid grid-cols-[120px_1fr] gap-2 px-2 py-1.5 text-xs font-mono">
            <span className="text-amber-300/90 truncate" title={k}>{k}</span>
            <span className="text-foreground/90 break-all">{headers[k]}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function BodyBlock({ title, body, size }: { title: string; body?: string; size: number }) {
  const isJson = body && (body.trim().startsWith('{') || body.trim().startsWith('['))
  return (
    <div>
      <div className="flex items-center justify-between mb-1.5">
        <span className="text-[10px] uppercase tracking-wider text-muted-foreground">
          {title} <span className="font-mono text-muted-foreground/70">({fmtBytes(size)})</span>
        </span>
        {body && (
          <Button
            variant="ghost"
            size="sm"
            className="h-6 px-2 text-[10px] gap-1 font-mono"
            onClick={() => { navigator.clipboard.writeText(body); toast.success('Body copied') }}
          >
            <Copy className="w-3 h-3" /> copy
          </Button>
        )}
      </div>
      <div className="rounded border border-border bg-muted/20 p-3 max-h-[280px] overflow-auto scrollbar-thin">
        {!body ? (
          <div className="text-xs text-muted-foreground font-mono italic">(empty)</div>
        ) : isJson ? (
          <pre
            className="text-[11px] leading-relaxed font-mono whitespace-pre-wrap break-all"
            dangerouslySetInnerHTML={{ __html: highlightJson(body) }}
          />
        ) : (
          <pre className="text-[11px] leading-relaxed font-mono whitespace-pre-wrap break-all text-foreground/90">{body}</pre>
        )}
      </div>
    </div>
  )
}
