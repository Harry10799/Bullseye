'use client'

import { useCallback, useMemo, useState, useEffect, memo } from 'react'
import ReactFlow, {
  Background, Controls, MiniMap, applyNodeChanges, applyEdgeChanges,
  MarkerType, type Node, type Edge, type NodeChange, type EdgeChange,
  type NodeProps, type EdgeProps, Handle, Position,
} from 'reactflow'
import dagre from 'dagre'
import { Network, AlertTriangle, Clock, Zap, Globe, Database, Cloud } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useTrafficStore } from '@/store/traffic-store'
import { computeEdgeStats, computeServiceStats } from '@/lib/stats'
import type { EdgeStats } from '@/lib/traffic-types'
import { protocolBadge, latencyColor } from '@/lib/ui'
import 'reactflow/dist/style.css'

// ---------- dagre layout ----------
const NODE_W = 200
const NODE_H = 96

function layoutNodes(nodes: LayoutNode[], edges: LayoutEdge[], direction: 'LR' | 'TB' = 'LR') {
  const g = new dagre.graphlib.Graph()
  g.setGraph({ rankdir: direction, ranksep: 90, nodesep: 36, marginx: 40, marginy: 40, edgesep: 24 })
  g.setDefaultEdgeLabel(() => ({}))
  for (const n of nodes) g.setNode(n.id, { width: NODE_W, height: NODE_H })
  for (const e of edges) g.setEdge(e.source, e.target)
  dagre.layout(g)
  return nodes.map(n => {
    const pos = g.node(n.id)
    return { ...n, position: { x: pos.x - NODE_W / 2, y: pos.y - NODE_H / 2 } }
  })
}

interface LayoutNode { id: string; position: { x: number; y: number } }
interface LayoutEdge { source: string; target: string }

// ---------- custom node ----------
interface ServiceNodeData {
  label: string
  namespace: string
  protocol: string
  incoming: number
  outgoing: number
  errors: number
  errorRate: number
  p95: number
  isExternal: boolean
  isFocused: boolean
  isFailing: boolean
  isSelected: boolean
  [key: string]: unknown
}

const ServiceNode = memo(({ data, selected }: NodeProps<ServiceNodeData>) => {
  const proto = protocolBadge(data.protocol as any)
  const errTone = data.errorRate > 0.1 ? 'rose' : data.errorRate > 0.01 ? 'amber' : 'emerald'
  const borderCls = {
    rose: 'border-rose-500/60 shadow-rose-500/20',
    amber: 'border-amber-500/60 shadow-amber-500/20',
    emerald: 'border-emerald-500/40 shadow-emerald-500/10',
  }[errTone]
  const dotCls = {
    rose: 'bg-rose-400',
    amber: 'bg-amber-400',
    emerald: 'bg-emerald-400',
  }[errTone]
  const Icon = data.isExternal ? Cloud : data.protocol === 'Redis' || data.protocol === 'Postgres' ? Database : data.protocol === 'DNS' ? Globe : Network

  return (
    <>
      <Handle type="target" position={Position.Left} style={{ background: 'oklch(0.4 0.02 230)', width: 6, height: 6, border: 'none' }} />
      <div
        className={`relative px-3 py-2.5 rounded-lg border-2 bg-card/95 backdrop-blur shadow-lg transition-all ${borderCls} ${
          selected ? 'ring-2 ring-primary ring-offset-2 ring-offset-background scale-[1.02]' : ''
        } ${data.isFocused ? '' : 'opacity-40'}`}
        style={{ width: NODE_W }}
      >
        {/* error pulse ring */}
        {data.isFailing && (
          <span className="absolute -inset-px rounded-lg border-2 border-rose-500/60 pointer-events-none pulse-ring" />
        )}
        <div className="flex items-center gap-2 mb-1.5">
          <div className={`w-6 h-6 rounded flex items-center justify-center ${proto.cls.split(' ').find(c => c.startsWith('bg-'))} ${proto.cls.split(' ').find(c => c.startsWith('text-'))}`}>
            <Icon className="w-3.5 h-3.5" />
          </div>
          <div className="flex-1 min-w-0">
            <div className="font-mono text-xs font-semibold truncate text-foreground">{data.label}</div>
            <div className="text-[9px] text-muted-foreground font-mono">{data.namespace}</div>
          </div>
          <span className={`w-2 h-2 rounded-full ${dotCls} ${data.isFailing ? 'pulse-ring' : ''}`} />
        </div>
        <div className="grid grid-cols-3 gap-1 text-[9px] font-mono">
          <div className="rounded bg-muted/40 px-1 py-0.5 text-center">
            <div className="text-muted-foreground uppercase tracking-wider">in</div>
            <div className="text-foreground font-semibold">{data.incoming}</div>
          </div>
          <div className="rounded bg-muted/40 px-1 py-0.5 text-center">
            <div className="text-muted-foreground uppercase tracking-wider">out</div>
            <div className="text-foreground font-semibold">{data.outgoing}</div>
          </div>
          <div className={`rounded px-1 py-0.5 text-center ${errTone === 'rose' ? 'bg-rose-500/15' : errTone === 'amber' ? 'bg-amber-500/15' : 'bg-emerald-500/10'}`}>
            <div className="text-muted-foreground uppercase tracking-wider">err</div>
            <div className={`font-semibold ${errTone === 'rose' ? 'text-rose-300' : errTone === 'amber' ? 'text-amber-300' : 'text-emerald-300'}`}>
              {(data.errorRate * 100).toFixed(0)}%
            </div>
          </div>
        </div>
        <div className="flex items-center justify-between mt-1.5 text-[9px] font-mono text-muted-foreground">
          <span>p95</span>
          <span className={latencyColor(data.p95)}>{data.p95}ms</span>
        </div>
      </div>
      <Handle type="source" position={Position.Right} style={{ background: 'oklch(0.4 0.02 230)', width: 6, height: 6, border: 'none' }} />
    </>
  )
})
ServiceNode.displayName = 'ServiceNode'

const nodeTypes = { service: ServiceNode }

// ---------- custom edge ----------
interface ServiceEdgeData {
  hits: number
  errors: number
  errorRate: number
  p95: number
  proto: string
  isFocused: boolean
  isHovered: boolean
  [key: string]: unknown
}

const ServiceEdge = ({ sourceX, sourceY, targetX, targetY, data, markerEnd }: EdgeProps<ServiceEdgeData>) => {
  const dx = targetX - sourceX
  const cx1 = sourceX + dx * 0.5
  const cx2 = sourceX + dx * 0.5
  const path = `M ${sourceX} ${sourceY} C ${cx1} ${sourceY}, ${cx2} ${targetY}, ${targetX} ${targetY}`

  const tone = data.errorRate > 0.1 ? 'rose' : data.errorRate > 0.01 ? 'amber' : 'emerald'
  const stroke = {
    rose: 'oklch(0.65 0.22 25)',
    amber: 'oklch(0.78 0.16 80)',
    emerald: 'oklch(0.75 0.18 145)',
  }[tone]
  const width = 1 + Math.min(5, Math.sqrt(data.hits) * 0.6)
  const opacity = data.isFocused ? 1 : 0.18
  const animated = data.isFocused && data.errorRate > 0
  const midX = (sourceX + targetX) / 2
  const midY = (sourceY + targetY) / 2

  return (
    <g opacity={opacity} style={{ transition: 'opacity 0.2s' }}>
      <path d={path} fill="none" stroke="transparent" strokeWidth={18} style={{ cursor: 'pointer' }} />
      <path
        d={path}
        fill="none"
        stroke={stroke}
        strokeWidth={width}
        markerEnd={markerEnd}
        className={animated ? 'flow-line' : ''}
        strokeLinecap="round"
      />
      {data.isHovered && (
        <foreignObject x={midX - 60} y={midY - 22} width={120} height={20} style={{ overflow: 'visible', pointerEvents: 'none' }}>
          <div className="bg-card border border-border rounded px-1.5 py-0.5 text-[9px] font-mono text-center text-foreground/90 shadow-lg">
            {data.hits} hits · {data.errors}e · {data.p95}ms
          </div>
        </foreignObject>
      )}
    </g>
  )
}

const edgeTypes = { service: ServiceEdge }

// ---------- helpers for adjacency ----------
function buildAdjacencyMap(edgeStats: EdgeStats[]): Map<string, Set<string>> {
  const m = new Map<string, Set<string>>()
  for (const e of edgeStats) {
    if (!m.has(e.src)) m.set(e.src, new Set())
    if (!m.has(e.dst)) m.set(e.dst, new Set())
    m.get(e.src)!.add(e.dst)
    m.get(e.dst)!.add(e.src)
  }
  return m
}

// ---------- main component ----------
export function ServiceMapTab() {
  const entries = useTrafficStore(s => s.entries)
  const topology = useTrafficStore(s => s.topology)
  const [hoveredNode, setHoveredNode] = useState<string | null>(null)
  const [hoveredEdge, setHoveredEdge] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [direction, setDirection] = useState<'LR' | 'TB'>('LR')
  // user-dragged position overrides — keyed by node id (state so changes trigger recompute)
  const [positionOverrides, setPositionOverrides] = useState<Map<string, { x: number; y: number }>>(new Map())
  // local nodes/edges so ReactFlow can manage changes (drag, selection)
  const [rfNodes, setRfNodes] = useState<Node[]>([])
  const [rfEdges, setRfEdges] = useState<Edge[]>([])

  const edgeStats = useMemo(() => computeEdgeStats(entries), [entries])
  const svcStats = useMemo(() => computeServiceStats(entries, topology || undefined), [entries, topology])
  const statsByName = useMemo(() => new Map(svcStats.map(s => [s.service, s])), [svcStats])
  const adjacency = useMemo(() => buildAdjacencyMap(edgeStats), [edgeStats])

  // build the node set from topology + observed
  const baseNodes = useMemo(() => {
    const nodeSet = new Map<string, { name: string; ns: string; protocol: string; isExternal: boolean }>()
    if (topology) {
      for (const s of topology.services) {
        nodeSet.set(s.name, { name: s.name, ns: s.ns, protocol: s.protocol, isExternal: s.kind === 'external' })
      }
    }
    for (const s of svcStats) {
      if (!nodeSet.has(s.service)) {
        nodeSet.set(s.service, { name: s.service, ns: s.ns, protocol: String(s.protocol), isExternal: s.ns === 'external' })
      }
    }
    return Array.from(nodeSet.values())
  }, [topology, svcStats])

  // dagre-computed base positions (recomputed only when structure or direction changes)
  const baseLayout = useMemo(() => {
    const layoutInput = baseNodes.map(n => ({ id: n.name, position: { x: 0, y: 0 } }))
    const layoutEdgesInput = edgeStats.map(e => ({ source: e.src, target: e.dst }))
    return layoutNodes(layoutInput, layoutEdgesInput, direction)
  }, [baseNodes, edgeStats, direction])
  const baseLayoutMap = useMemo(() => new Map(baseLayout.map(n => [n.id, n.position])), [baseLayout])

  // pure compute of ReactFlow nodes/edges from inputs
  const computedNodes: Node[] = useMemo(() => {
    return baseNodes.map(n => {
      const stat = statsByName.get(n.name)
      const errRate = stat?.errorRate || 0
      const override = positionOverrides.get(n.name)
      const isSelected = n.name === selected
      const isFocused = !selected || n.name === selected || adjacency.get(selected)?.has(n.name)
      return {
        id: n.name,
        type: 'service',
        position: override || baseLayoutMap.get(n.name) || { x: 0, y: 0 },
        data: {
          label: n.name,
          namespace: n.ns,
          protocol: n.protocol,
          incoming: stat?.incoming || 0,
          outgoing: stat?.outgoing || 0,
          errors: stat?.errors || 0,
          errorRate: errRate,
          p95: stat?.p95 || 0,
          isExternal: n.isExternal,
          isFailing: errRate > 0.05,
          isSelected,
          isFocused,
        } as ServiceNodeData,
        draggable: true,
      } as Node
    })
  }, [baseNodes, baseLayoutMap, statsByName, selected, adjacency, positionOverrides])

  const computedEdges: Edge[] = useMemo(() => {
    return edgeStats.map((e, i) => {
      const tone = e.errorRate > 0.1 ? 'rose' : e.errorRate > 0.01 ? 'amber' : 'emerald'
      const color = { rose: 'oklch(0.65 0.22 25)', amber: 'oklch(0.78 0.16 80)', emerald: 'oklch(0.75 0.18 145)' }[tone]
      const isFocused = !selected || e.src === selected || e.dst === selected
      const edgeId = `e-${i}-${e.src}-${e.dst}`
      return {
        id: edgeId,
        source: e.src,
        target: e.dst,
        type: 'service',
        animated: isFocused && e.errorRate > 0,
        markerEnd: { type: MarkerType.ArrowClosed, color, width: 14, height: 14 },
        data: {
          hits: e.hits, errors: e.errors, errorRate: e.errorRate, p95: e.p95,
          proto: e.proto, isFocused, isHovered: edgeId === hoveredEdge,
        } as ServiceEdgeData,
        focusable: true,
      } as Edge
    })
  }, [edgeStats, selected, hoveredEdge])

  // sync computed → local ReactFlow state when the meaningful signature changes.
  // This is the canonical ReactFlow pattern for bridging controlled computed
  // state with ReactFlow's internal node/edge management.
  // Position is included in the signature so direction-toggle / re-layout actually
  // propagate to ReactFlow's internal node state.
  const nodesSig = useMemo(() => computedNodes.map(n => `${n.id}:${Math.round(n.position.x)}:${Math.round(n.position.y)}:${(n.data as ServiceNodeData).errorRate}:${(n.data as ServiceNodeData).incoming}:${(n.data as ServiceNodeData).outgoing}:${(n.data as ServiceNodeData).errors}:${(n.data as ServiceNodeData).p95}:${(n.data as ServiceNodeData).isFocused}:${(n.data as ServiceNodeData).isSelected}`).join('||'), [computedNodes])
  const edgesSig = useMemo(() => computedEdges.map(e => `${e.id}:${(e.data as ServiceEdgeData).hits}:${(e.data as ServiceEdgeData).errors}:${(e.data as ServiceEdgeData).errorRate}:${(e.data as ServiceEdgeData).p95}:${(e.data as ServiceEdgeData).isFocused}:${(e.data as ServiceEdgeData).isHovered}:${e.animated}`).join('||'), [computedEdges])

  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    setRfNodes(computedNodes)
  }, [nodesSig])

  useEffect(() => {
    setRfEdges(computedEdges)
  }, [edgesSig])
  /* eslint-enable react-hooks/set-state-in-effect */

  const onNodesChange = useCallback((changes: NodeChange[]) => {
    setRfNodes(nds => applyNodeChanges(changes, nds))
  }, [])

  const onNodeDragStop = useCallback((_: any, node: Node) => {
    setPositionOverrides(prev => {
      const next = new Map(prev)
      next.set(node.id, node.position)
      return next
    })
  }, [])

  const onEdgesChange = useCallback((changes: EdgeChange[]) => {
    setRfEdges(eds => applyEdgeChanges(changes, eds))
  }, [])

  const onNodeClick = useCallback((_: any, node: Node) => {
    setSelected(s => s === node.id ? null : node.id)
  }, [])

  const onNodeMouseEnter = useCallback((_: any, node: Node) => setHoveredNode(node.id), [])
  const onNodeMouseLeave = useCallback(() => setHoveredNode(null), [])
  const onEdgeMouseEnter = useCallback((_: any, edge: Edge) => setHoveredEdge(edge.id), [])
  const onEdgeMouseLeave = useCallback(() => setHoveredEdge(null), [])

  const focusNode = hoveredNode || selected
  const focusStats = focusNode ? statsByName.get(focusNode) : undefined
  const focusEdges = focusNode
    ? edgeStats.filter(e => e.src === focusNode || e.dst === focusNode)
    : edgeStats

  const relayout = () => {
    setPositionOverrides(new Map())
    setSelected(null)
  }

  const toggleDirection = () => {
    setPositionOverrides(new Map())
    setDirection(d => d === 'LR' ? 'TB' : 'LR')
  }

  const nodeColor = (n: Node) => {
    const data = n.data as ServiceNodeData
    if (data.errorRate > 0.1) return 'oklch(0.65 0.22 25)'
    if (data.errorRate > 0.01) return 'oklch(0.78 0.16 80)'
    return 'oklch(0.75 0.18 145)'
  }

  return (
    <div className="flex h-full min-h-0">
      {/* Graph */}
      <div className="flex-1 relative overflow-hidden">
        {/* Top-right legend / stats */}
        <div className="absolute top-3 right-3 z-10 flex flex-col gap-1.5">
          <Badge variant="outline" className="font-mono text-[11px] justify-between gap-2 border-border bg-card/80 backdrop-blur">
            <span className="text-muted-foreground">services</span>
            <span>{baseNodes.length}</span>
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

        {/* Top-left controls */}
        <div className="absolute top-3 left-3 z-10 flex items-center gap-1.5">
          <Button
            variant="outline"
            size="sm"
            className="h-8 gap-1.5 font-mono text-xs bg-card/80 backdrop-blur"
            onClick={relayout}
          >
            <Network className="w-3.5 h-3.5" />
            re-layout
          </Button>
          <Button
            variant="outline"
            size="sm"
            className={`h-8 gap-1.5 font-mono text-xs bg-card/80 backdrop-blur ${direction === 'LR' ? 'border-primary/40 text-primary' : ''}`}
            onClick={toggleDirection}
          >
            {direction === 'LR' ? 'horizontal' : 'vertical'}
          </Button>
        </div>

        {/* Legend bottom-left */}
        <div className="absolute bottom-3 left-3 z-10 bg-card/80 backdrop-blur border border-border rounded p-2 space-y-1.5">
          <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono">edge color = error rate</div>
          <div className="flex items-center gap-2">
            <div className="w-8 h-1 rounded" style={{ background: 'oklch(0.75 0.18 145)' }} />
            <span className="text-[10px] font-mono">0%</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="w-8 h-1 rounded" style={{ background: 'oklch(0.78 0.16 80)' }} />
            <span className="text-[10px] font-mono">1–10%</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="w-8 h-1 rounded" style={{ background: 'oklch(0.65 0.22 25)' }} />
            <span className="text-[10px] font-mono">&gt; 10%</span>
          </div>
          <div className="pt-1 border-t border-border mt-1.5">
            <div className="text-[10px] uppercase tracking-wider text-muted-foreground font-mono">node size = traffic</div>
          </div>
        </div>

        <ReactFlow
          nodes={rfNodes}
          edges={rfEdges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={onNodeClick}
          onNodeMouseEnter={onNodeMouseEnter}
          onNodeMouseLeave={onNodeMouseLeave}
          onNodeDragStop={onNodeDragStop}
          onEdgeMouseEnter={onEdgeMouseEnter}
          onEdgeMouseLeave={onEdgeMouseLeave}
          fitView
          fitViewOptions={{ padding: 0.15, maxZoom: 1.4 }}
          minZoom={0.2}
          maxZoom={2.5}
          proOptions={{ hideAttribution: true }}
          defaultEdgeOptions={{ type: 'service' }}
          className="bg-background"
        >
          <Background color="oklch(0.4 0.02 230)" gap={24} size={1.4} />
          <Controls
            className="!bg-card/80 !backdrop-blur !border !border-border !rounded !shadow-lg"
            showInteractive={false}
          />
          <MiniMap
            nodeColor={nodeColor}
            nodeStrokeWidth={2}
            nodeStrokeColor="oklch(0.18 0.012 230)"
            maskColor="oklch(0.18 0.012 230 / 0.7)"
            className="!bg-card/80 !backdrop-blur !border !border-border !rounded"
            pannable
            zoomable
          />
        </ReactFlow>
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
            <p className="text-xs text-muted-foreground">Hover or click a node to inspect. Drag to rearrange; scroll to zoom.</p>
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
