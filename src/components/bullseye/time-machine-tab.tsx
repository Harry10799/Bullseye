'use client'

import { useEffect, useMemo, useRef, useState } from 'react'
import { Clock, Play, Pause, SkipBack, SkipForward, Camera, Repeat, Rewind } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Slider } from '@/components/ui/slider'
import { useTrafficStore, getEntryById } from '@/store/traffic-store'
import { DetailPanel } from './detail-panel'
import { fmtTime, statusColor, latencyColor, protocolBadge } from '@/lib/ui'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

export function TimeMachineTab() {
  const entries = useTrafficStore(s => s.entries)
  const historySnapshot = useTrafficStore(s => s.historySnapshot)
  const replayIndex = useTrafficStore(s => s.replayIndex)
  const takeSnapshot = useTrafficStore(s => s.takeHistorySnapshot)
  const setReplayIndex = useTrafficStore(s => s.setReplayIndex)
  const select = useTrafficStore(s => s.select)
  const selectedId = useTrafficStore(s => s.selectedId)
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState(1)

  // Auto-take snapshot when entering the tab if none exists
  useEffect(() => {
    if (!historySnapshot && entries.length > 0) {
      takeSnapshot()
    }
  }, [historySnapshot, entries.length, takeSnapshot])

  const snapshot = historySnapshot || []
  const total = snapshot.length

  // play loop
  const playRef = useRef<number | null>(null)
  useEffect(() => {
    if (!playing) {
      if (playRef.current) {
        cancelAnimationFrame(playRef.current)
        playRef.current = null
      }
      return
    }
    let lastTs = performance.now()
    const tick = (now: number) => {
      const dt = now - lastTs
      lastTs = now
      const step = Math.max(1, Math.floor(dt * speed * 0.05))
      const cur = useTrafficStore.getState().replayIndex ?? 0
      const next = cur + step
      if (next >= total - 1) {
        setReplayIndex(total - 1)
        setPlaying(false)
        return
      }
      setReplayIndex(next)
      playRef.current = requestAnimationFrame(tick)
    }
    playRef.current = requestAnimationFrame(tick)
    return () => {
      if (playRef.current) cancelAnimationFrame(playRef.current)
    }
  }, [playing, speed, total, setReplayIndex])

  // visible entries up to current replay index
  const currentIdx = replayIndex ?? Math.max(0, total - 1)
  const visibleEntries = useMemo(() => {
    return snapshot.slice(Math.max(0, currentIdx - 60), currentIdx + 1).reverse()
  }, [snapshot, currentIdx])

  // histogram data — 40 buckets across the snapshot timeline
  const histogram = useMemo(() => {
    if (snapshot.length === 0) return [] as { t: number; count: number; errors: number }[]
    const buckets = 60
    const min = snapshot[0].timestamp
    const max = snapshot[snapshot.length - 1].timestamp
    const span = Math.max(1, max - min)
    const arr = Array.from({ length: buckets }, () => ({ t: 0, count: 0, errors: 0 }))
    for (const e of snapshot) {
      const idx = Math.min(buckets - 1, Math.floor(((e.timestamp - min) / span) * buckets))
      arr[idx].t = min + (idx / buckets) * span
      arr[idx].count++
      if ((e.protocol === 'HTTP' && (e.status ?? 0) >= 500) ||
          (e.protocol === 'gRPC' && (e.status ?? 0) > 0) ||
          (e.protocol === 'DNS' && e.dnsRcode !== 'NOERROR')) {
        arr[idx].errors++
      }
    }
    return arr
  }, [snapshot])

  const maxBucket = Math.max(1, ...histogram.map(b => b.count))

  const currentEntry = snapshot[currentIdx]
  const startTime = snapshot[0]?.timestamp
  const endTime = snapshot[snapshot.length - 1]?.timestamp
  const curTime = currentEntry?.timestamp

  const handleScrub = (val: number[]) => {
    setPlaying(false)
    setReplayIndex(val[0])
  }

  const handleResnapshot = () => {
    takeSnapshot()
    setPlaying(false)
    setReplayIndex(null)
  }

  return (
    <div className="flex h-full min-h-0">
      <div className="flex-1 flex flex-col min-w-0">
        {/* Top: timeline histogram + scrubber */}
        <div className="border-b border-border bg-card/30 p-3 space-y-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Clock className="w-4 h-4 text-primary" />
              <span className="font-mono text-xs uppercase tracking-wider text-muted-foreground">time machine</span>
              <Badge variant="outline" className="font-mono text-[11px] border-border">
                {total} packets · {snapshot.length > 0 ? `${((endTime! - startTime!) / 1000).toFixed(1)}s` : '0s'} window
              </Badge>
            </div>
            <Button variant="outline" size="sm" className="h-8 gap-1.5 font-mono text-xs" onClick={handleResnapshot}>
              <Camera className="w-3.5 h-3.5" />
              re-snapshot
            </Button>
          </div>

          {/* Histogram */}
          <div className="relative h-16">
            <div className="absolute inset-0 flex items-end gap-px">
              {histogram.map((b, i) => {
                const h = (b.count / maxBucket) * 100
                const errH = b.count > 0 ? (b.errors / b.count) * h : 0
                const isPast = i <= Math.floor((currentIdx / Math.max(1, total - 1)) * histogram.length)
                return (
                  <div key={i} className="flex-1 relative flex flex-col justify-end" style={{ height: '100%' }}>
                    <div
                      className="w-full bg-emerald-500/30 rounded-t-sm"
                      style={{ height: `${h}%`, opacity: isPast ? 1 : 0.3 }}
                    />
                    {b.errors > 0 && (
                      <div
                        className="absolute bottom-0 w-full bg-rose-500/60 rounded-t-sm"
                        style={{ height: `${errH}%` }}
                      />
                    )}
                  </div>
                )
              })}
            </div>
            {/* playhead line */}
            {total > 0 && (
              <div
                className="absolute top-0 bottom-0 w-px bg-primary pointer-events-none"
                style={{ left: `${(currentIdx / Math.max(1, total - 1)) * 100}%` }}
              >
                <div className="absolute -top-1 -translate-x-1/2 w-2 h-2 rounded-full bg-primary shadow-lg shadow-primary/50" />
              </div>
            )}
          </div>

          {/* Scrubber */}
          <div className="flex items-center gap-3">
            <span className="font-mono text-[11px] text-muted-foreground tabular-nums w-20">
              {curTime ? fmtTime(curTime) : '—'}
            </span>
            <Slider
              value={[currentIdx]}
              min={0}
              max={Math.max(0, total - 1)}
              step={1}
              onValueChange={handleScrub}
              className="flex-1"
            />
            <span className="font-mono text-[11px] text-muted-foreground tabular-nums w-20 text-right">
              {endTime ? fmtTime(endTime) : '—'}
            </span>
          </div>

          {/* Transport controls */}
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-1.5">
              <Button
                variant="outline"
                size="sm"
                className="h-8 w-8 p-0"
                onClick={() => { setPlaying(false); setReplayIndex(0) }}
                disabled={total === 0}
              >
                <SkipBack className="w-3.5 h-3.5" />
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="h-8 w-8 p-0"
                onClick={() => { setPlaying(false); setReplayIndex(Math.max(0, currentIdx - 10)) }}
                disabled={total === 0}
              >
                <Rewind className="w-3.5 h-3.5" />
              </Button>
              <Button
                variant={playing ? 'default' : 'outline'}
                size="sm"
                className="h-8 gap-1.5 font-mono text-xs"
                onClick={() => setPlaying(p => !p)}
                disabled={total === 0 || currentIdx >= total - 1}
              >
                {playing ? <Pause className="w-3.5 h-3.5" /> : <Play className="w-3.5 h-3.5" />}
                {playing ? 'pause' : 'play'}
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="h-8 w-8 p-0"
                onClick={() => { setPlaying(false); setReplayIndex(Math.min(total - 1, currentIdx + 10)) }}
                disabled={total === 0}
              >
                <SkipForward className="w-3.5 h-3.5" />
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="h-8 gap-1.5 font-mono text-xs"
                onClick={() => { setPlaying(false); setReplayIndex(null) }}
              >
                <Repeat className="w-3.5 h-3.5" />
                back to live
              </Button>
            </div>

            <div className="flex items-center gap-2">
              <span className="text-[11px] font-mono text-muted-foreground">speed</span>
              <Select value={String(speed)} onValueChange={v => setSpeed(Number(v))}>
                <SelectTrigger className="h-8 w-[80px] font-mono text-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="0.5" className="font-mono text-xs">0.5×</SelectItem>
                  <SelectItem value="1" className="font-mono text-xs">1×</SelectItem>
                  <SelectItem value="2" className="font-mono text-xs">2×</SelectItem>
                  <SelectItem value="5" className="font-mono text-xs">5×</SelectItem>
                  <SelectItem value="10" className="font-mono text-xs">10×</SelectItem>
                </SelectContent>
              </Select>
              <span className="text-[11px] font-mono text-muted-foreground ml-2">
                pos: {currentIdx} / {total - 1}
              </span>
            </div>
          </div>
        </div>

        {/* Mini live feed for the replay window */}
        <div className="grid grid-cols-[110px_60px_180px_180px_70px_1fr_60px_70px] gap-2 px-3 py-2 border-b border-border bg-muted/30 text-[10px] font-mono uppercase tracking-wider text-muted-foreground">
          <div>time</div>
          <div>proto</div>
          <div>source</div>
          <div>destination</div>
          <div>method</div>
          <div>path / query</div>
          <div className="text-right">status</div>
          <div className="text-right">latency</div>
        </div>
        <div className="flex-1 overflow-auto scrollbar-thin min-h-0">
          {visibleEntries.length === 0 ? (
            <div className="p-8 text-center text-sm text-muted-foreground font-mono">
              No history yet — taking snapshot of current buffer…
            </div>
          ) : (
            visibleEntries.map(e => {
              const proto = protocolBadge(e.protocol)
              const isSelected = e.id === selectedId
              return (
                <button
                  key={e.id}
                  onClick={() => select(isSelected ? null : e.id)}
                  className={`w-full text-left grid grid-cols-[110px_60px_180px_180px_70px_1fr_60px_70px] gap-2 px-3 py-1.5 border-b border-border/50 text-xs font-mono transition-colors ${
                    isSelected ? 'bg-primary/15 border-l-2 border-l-primary' : 'hover:bg-muted/40'
                  }`}
                >
                  <div className="text-muted-foreground tabular-nums">{fmtTime(e.timestamp)}</div>
                  <div>
                    <Badge variant="outline" className={`text-[9px] px-1 py-0 h-4 border ${proto.cls} font-mono`}>
                      {proto.label}
                    </Badge>
                  </div>
                  <div className="truncate text-foreground/90">{e.source.service}</div>
                  <div className="truncate text-foreground/90">{e.destination.service}</div>
                  <div className="text-foreground/80 truncate">{e.method || '—'}</div>
                  <div className="text-foreground truncate">{e.protocol === 'DNS' ? e.dnsQuery : e.path || '—'}</div>
                  <div className={`text-right tabular-nums ${statusColor(e.status, e.protocol)}`}>
                    {e.protocol === 'gRPC' ? (e.status === 0 ? 'OK' : e.status) : (e.status ?? '—')}
                  </div>
                  <div className={`text-right tabular-nums ${latencyColor(e.latencyMs)}`}>
                    {e.latencyMs < 1000 ? `${e.latencyMs}ms` : `${(e.latencyMs / 1000).toFixed(2)}s`}
                  </div>
                </button>
              )
            })
          )}
        </div>
      </div>

      <aside className="w-[420px] border-l border-border flex-shrink-0 hidden lg:flex flex-col">
        <DetailPanel entryId={selectedId} />
      </aside>
    </div>
  )
}
