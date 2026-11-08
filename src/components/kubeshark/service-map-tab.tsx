'use client'

import { useMemo, useState } from 'react'
import { Network, AlertTriangle, Clock, Zap } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { useTrafficStore } from '@/store/traffic-store'
import { computeEdgeStats, computeServiceStats, isHttpError } from '@/lib/stats'
import type { ServiceStats, EdgeStats } from '@/lib/traffic-types'
import { protocolBadge, latencyColor } from '@/lib/ui'

export function ServiceMapTab() {
  const entries = useTrafficStore(s => s.entries)
  const topology = useTrafficStore(s => s.topology)
  const [hovered, setHovered] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)

  const edgeStats = useMemo(() => computeEdgeStats(entries), [entries])
  const svcStats = useMemo(() => computeServiceStats(entries, topology || undefined), [entries, topology])
  const statsByName = useMemo(() => new Map(svcStats.map(s => [s.service, s])), [svcStats])

  // build the node set from topology + observed
  const nodes = useMemo(() => {
    const nodeSet = new Map<string, { name: string; ns: string; protocol: string }>()
    if (topology) {
      for (const s of topology.services) {
        nodeSet.set(s.name, { name: s.name, ns: s.ns, protocol: s.protocol })
      }
    }
    for (const s of svcStats) {
      if (!nodeSet.has(s.service)) nodeSet.set(s.service, { name: s.service, ns: s.ns, protocol: String(s.protocol) })
    }
    return Array.from(nodeSet.values())
  }, [topology, svcStats])

  // Compute positions: ring layout with the "external" / "kube-system" nodes on the periphery
  const positions = useMemo(() => {
    const W = 1100, H = 700
    const cx = W / 2, cy = H / 2
    const pos = new Map<string, { x: number; y: number }>()

    // group by namespace — arrange each namespace as a cluster on the ring
    const nsGroups = new Map<string, string[]>()
    for (const n of nodes) {
      if (!nsGroups.has(n.ns)) nsGroups.set(n.ns, [])
      nsGroups.get(n.ns)!.push(n.name)
    }
    const namespaces = Array.from(nsGroups.keys())
    const innerR = 220, outerR = 290
    namespaces.forEach((ns, i) => {
      const angle = (i / namespaces.length) * Math.PI * 2 - Math.PI / 2
      const arr = nsGroups.get(ns)!
      arr.forEach((name, j) => {
        const subAngle = angle + (j - (arr.length - 1) / 2) * 0.18
        const r = arr.length === 1 ? innerR : (j % 2 === 0 ? innerR : outerR)
        pos.set(name, {
          x: cx + r * Math.cos(subAngle),
          y: cy + r * Math.sin(subAngle),
        })
      })
    })

    return { pos, W, H, cx, cy }
  }, [nodes])

  const maxHits = Math.max(1, ...edgeStats.map(e => e.hits))

  const focusNode = hovered || selected
  const focusStats = focusNode ? statsByName.get(focusNode) : undefined
  const focusEdges = focusNode
    ? edgeStats.filter(e => e.src === focusNode || e.dst === focusNode)
    : edgeStats

  return (
    <div className="flex h-full min-h-0">
      {/* Graph */}
      <div className="flex-1 relative overflow-hidden grid-bg">
        {/* Top-right legend / stats */}
        <div className="absolute top-3 right-3 z-10 flex flex-col gap-1.5">
          <Badge variant="outline" className="font-mono text-[11px] justify-between gap-2 border-border bg-card/80 backdrop-blur">
            <span className="text-muted-foreground">services</span>
            <span>{nodes.length}</span>
          </Badge>
          <Badge variant="outline" className="font-mono text-[11px] justify-between gap-2 border-border bg-card/80 backdrop-blur">
            <span className="text-muted-foreground">edges</span>
            <span>{edgeStats.length}</span>
          </Badge>
          <Badge variant="outline" className="font-mono text-[11px] justify-between gap-2 border-border bg-card/80 backdrop-blur">
            <span className="text-muted-foreground">errors</span>
            <span className="text-rose-300">{edgeStats.reduce((s, e) => s + e.errors, 0)}</span>
          </Badge>
        </div>

        {/* Legend bottom-left */}
        <div className="absolute bottom-3 left-3 z-10 bg-card/80 backdrop-blur border border-border rounded p-2 space-y-1.5">
          <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono">edge color = error rate</div>
          <div className="flex items-center gap-2">
            <div className="w-8 h-1 rounded bg-emerald-500" />
            <span className="text-[10px] font-mono">0%</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="w-8 h-1 rounded bg-amber-500" />
            <span className="text-[10px] font-mono">1–10%</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="w-8 h-1 rounded bg-rose-500" />
            <span className="text-[10px] font-mono">&gt; 10%</span>
          </div>
          <div className="pt-1 border-t border-border mt-1.5">
            <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono">node size = traffic</div>
          </div>
        </div>

        <svg viewBox={`0 0 ${positions.W} ${positions.H}`} className="w-full h-full">
          <defs>
            <marker id="arrow-emerald" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="5" markerHeight="5" orient="auto">
              <path d="M 0 0 L 10 5 L 0 10 z" fill="oklch(0.75 0.18 145)" />
            </marker>
            <marker id="arrow-amber" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="5" markerHeight="5" orient="auto">
              <path d="M 0 0 L 10 5 L 0 10 z" fill="oklch(0.78 0.16 80)" />
            </marker>
            <marker id="arrow-rose" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="5" markerHeight="5" orient="auto">
              <path d="M 0 0 L 10 5 L 0 10 z" fill="oklch(0.65 0.22 25)" />
            </marker>
            <radialGradient id="nodeGrad">
              <stop offset="0%" stopColor="oklch(0.3 0.02 230)" />
              <stop offset="100%" stopColor="oklch(0.22 0.013 230)" />
            </radialGradient>
            <radialGradient id="nodeGradError">
              <stop offset="0%" stopColor="oklch(0.35 0.18 25)" />
              <stop offset="100%" stopColor="oklch(0.22 0.05 25)" />
            </radialGradient>
          </defs>

          {/* Edges */}
          {focusEdges.map((e, i) => {
            const src = positions.pos.get(e.src)
            const dst = positions.pos.get(e.dst)
            if (!src || !dst) return null
            const isFocused = !focusNode || e.src === focusNode || e.dst === focusNode
            const opacity = isFocused ? 0.85 : 0.15
            const color = e.errorRate > 0.1 ? 'oklch(0.65 0.22 25)' : e.errorRate > 0.01 ? 'oklch(0.78 0.16 80)' : 'oklch(0.75 0.18 145)'
            const marker = e.errorRate > 0.1 ? 'url(#arrow-rose)' : e.errorRate > 0.01 ? 'url(#arrow-amber)' : 'url(#arrow-emerald)'
            const width = 1 + (e.hits / maxHits) * 4
            // shorten line so it doesn't overlap nodes
            const dx = dst.x - src.x, dy = dst.y - src.y
            const dist = Math.sqrt(dx * dx + dy * dy)
            const r1 = 26, r2 = 30
            const x1 = src.x + (dx / dist) * r1
            const y1 = src.y + (dy / dist) * r1
            const x2 = dst.x - (dx / dist) * r2
            const y2 = dst.y - (dy / dist) * r2
            const isFlowing = isFocused && e.errorRate > 0
            return (
              <g key={i} opacity={opacity} style={{ transition: 'opacity 0.2s' }}>
                <line
                  x1={x1} y1={y1} x2={x2} y2={y2}
                  stroke={color}
                  strokeWidth={width}
                  markerEnd={marker}
                  className={isFlowing ? 'flow-line' : ''}
                  strokeLinecap="round"
                />
                {/* edge label on hover */}
                {(focusNode && isFocused) && (
                  <text
                    x={(x1 + x2) / 2}
                    y={(y1 + y2) / 2 - 4}
                    fill="oklch(0.66 0.012 230)"
                    fontSize="9"
                    fontFamily="monospace"
                    textAnchor="middle"
                  >
                    {e.hits} hits · {e.errors} err · p95 {e.p95}ms
                  </text>
                )}
              </g>
            )
          })}

          {/* Nodes */}
          {nodes.map(n => {
            const pos = positions.pos.get(n.name)
            if (!pos) return null
            const stat = statsByName.get(n.name)
            const hits = stat ? stat.incoming + stat.outgoing : 0
            const errRate = stat?.errorRate || 0
            const r = 16 + Math.min(20, Math.sqrt(hits) * 1.5)
            const isFocused = !focusNode || focusNode === n.name
            const isError = errRate > 0.05
            const grad = isError ? 'url(#nodeGradError)' : 'url(#nodeGrad)'
            const stroke = isError ? 'oklch(0.65 0.22 25)' : errRate > 0.01 ? 'oklch(0.78 0.16 80)' : 'oklch(0.7 0.15 180)'
            const proto = protocolBadge(n.protocol as any)
            return (
              <g
                key={n.name}
                transform={`translate(${pos.x}, ${pos.y})`}
                onMouseEnter={() => setHovered(n.name)}
                onMouseLeave={() => setHovered(null)}
                onClick={() => setSelected(selected === n.name ? null : n.name)}
                style={{ cursor: 'pointer', opacity: isFocused ? 1 : 0.3, transition: 'opacity 0.2s' }}
              >
                {selected === n.name && (
                  <circle r={r + 6} fill="none" stroke="oklch(0.7 0.15 180)" strokeWidth="1.5" strokeDasharray="3 2" />
                )}
                {isError && (
                  <circle r={r + 4} fill="none" stroke="oklch(0.65 0.22 25)" strokeWidth="1" opacity="0.5" className="pulse-ring" />
                )}
                <circle r={r} fill={grad} stroke={stroke} strokeWidth={selected === n.name ? 2.5 : 1.5} />
                <text y={4} fontSize="9" fontFamily="monospace" fill="oklch(0.96 0.005 230)" textAnchor="middle" fontWeight="600">
                  {n.name.length > 14 ? n.name.slice(0, 12) + '…' : n.name}
                </text>
                <text y={r + 12} fontSize="8" fontFamily="monospace" fill="oklch(0.55 0.012 230)" textAnchor="middle">
                  {n.ns}
                </text>
              </g>
            )
          })}
        </svg>
      </div>

      {/* Side panel: focused node stats */}
      <aside className="w-[340px] border-l border-border flex-shrink-0 bg-card/30 overflow-auto scrollbar-thin">
        <div className="p-3 border-b border-border">
          <div className="flex items-center gap-2 mb-1">
            <Network className="w-4 h-4 text-primary" />
            <span className="font-mono text-xs uppercase tracking-wider text-muted-foreground">
              {focusNode ? 'service details' : 'cluster overview'}
            </span>
          </div>
          {focusNode ? (
            <>
              <h3 className="font-mono font-semibold text-sm">{focusNode}</h3>
              {focusStats && (
                <span className="text-[11px] text-muted-foreground font-mono">{focusStats.ns} · {focusStats.protocol}</span>
              )}
            </>
          ) : (
            <p className="text-xs text-muted-foreground">Hover or click a node to inspect</p>
          )}
        </div>

        {focusNode && focusStats && (
          <div className="p-3 space-y-3">
            <div className="grid grid-cols-2 gap-2">
              <StatTile label="incoming" value={focusStats.incoming.toString()} icon={<Zap className="w-3 h-3" />} />
              <StatTile label="outgoing" value={focusStats.outgoing.toString()} icon={<Zap className="w-3 h-3" />} />
              <StatTile label="errors" value={focusStats.errors.toString()} icon={<AlertTriangle className="w-3 h-3" />} valueClass={focusStats.errors > 0 ? 'text-rose-400' : 'text-emerald-400'} />
              <StatTile label="err %" value={(focusStats.errorRate * 100).toFixed(1) + '%'} icon={<AlertTriangle className="w-3 h-3" />} valueClass={focusStats.errorRate > 0.05 ? 'text-rose-400' : focusStats.errorRate > 0 ? 'text-amber-400' : 'text-emerald-400'} />
              <StatTile label="p95" value={focusStats.p95 + 'ms'} icon={<Clock className="w-3 h-3" />} valueClass={latencyColor(focusStats.p95)} />
              <StatTile label="proto" value={focusStats.protocol} icon={null} />
            </div>

            <div>
              <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono mb-1.5">connections</div>
              <div className="space-y-1">
                {focusEdges.slice(0, 12).map((e, i) => (
                  <EdgeRow key={i} e={e} focus={focusNode} />
                ))}
                {focusEdges.length === 0 && <div className="text-xs text-muted-foreground font-mono italic">no edges observed</div>}
              </div>
            </div>
          </div>
        )}

        {!focusNode && (
          <div className="p-3 space-y-2">
            <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono mb-1.5">top services by traffic</div>
            {svcStats.slice(0, 10).map(s => (
              <button
                key={s.service}
                onClick={() => setSelected(s.service)}
                className="w-full text-left flex items-center justify-between px-2 py-1.5 rounded border border-border bg-muted/20 hover:bg-primary/10 hover:border-primary/40 transition-colors"
              >
                <div className="min-w-0">
                  <div className="font-mono text-xs truncate">{s.service}</div>
                  <div className="text-[10px] text-muted-foreground font-mono">{s.ns}</div>
                </div>
                <div className="flex items-center gap-2 text-[10px] font-mono">
                  <span className="text-amber-300">{s.incoming + s.outgoing}</span>
                  {s.errors > 0 && <span className="text-rose-300">{s.errors}e</span>}
                </div>
              </button>
            ))}
          </div>
        )}
      </aside>
    </div>
  )
}

function StatTile({ label, value, icon, valueClass }: { label: string; value: string; icon: React.ReactNode; valueClass?: string }) {
  return (
    <div className="border border-border rounded bg-muted/20 p-2">
      <div className="flex items-center gap-1 text-[9px] uppercase tracking-wider text-muted-foreground font-mono mb-1">
        {icon}{label}
      </div>
      <div className={`font-mono text-sm font-semibold ${valueClass || ''}`}>{value}</div>
    </div>
  )
}

function EdgeRow({ e, focus }: { e: EdgeStats; focus: string }) {
  const other = e.src === focus ? e.dst : e.src
  const direction = e.src === focus ? '→' : '←'
  const color = e.errorRate > 0.1 ? 'text-rose-300' : e.errorRate > 0.01 ? 'text-amber-300' : 'text-emerald-300'
  return (
    <div className="flex items-center justify-between text-[11px] font-mono px-2 py-1 rounded bg-muted/20 border border-border/60">
      <div className="flex items-center gap-1.5 min-w-0">
        <span className="text-muted-foreground">{direction}</span>
        <span className="truncate">{other}</span>
      </div>
      <div className={`flex items-center gap-1.5 ${color}`}>
        <span>{e.hits}</span>
        {e.errors > 0 && <span className="text-rose-300/80">({e.errors}e)</span>}
        <span className="text-muted-foreground/70">{e.p95}ms</span>
      </div>
    </div>
  )
}
