'use client'

import { create } from 'zustand'
import { io, Socket } from 'socket.io-client'
import type { TrafficEntry, ClusterTopology } from '@/lib/traffic-types'

const MAX_BUFFER = 1500  // ring buffer size

// Source: 'simulator' (TypeScript mini-service) or 'hub' (Go backend).
// In production with the Go backend running, set NEXT_PUBLIC_BACKEND=hub
// to connect the UI directly to the Go hub's WebSocket on :3030.
const BACKEND = (process.env.NEXT_PUBLIC_BACKEND as 'simulator' | 'hub') || 'simulator'

// Simulator mini-service port (TS, socket.io on :3030 via Nginx XTransformPort)
const SIM_PORT = 3030
// Go hub port (raw WebSocket on :3030 — no XTransformPort needed when running
// the hub locally outside the Next.js dev server's Nginx gateway)
const HUB_WS_URL = process.env.NEXT_PUBLIC_HUB_WS_URL || 'ws://localhost:3030/'

interface TrafficState {
  entries: TrafficEntry[]  // newest first
  topology: ClusterTopology | null
  connected: boolean
  capturing: boolean
  selectedId: string | null
  // history snapshot for time-machine
  historySnapshot: TrafficEntry[] | null
  replayIndex: number | null  // null = not replaying
  // actions
  connect: () => void
  disconnect: () => void
  setCapturing: (on: boolean) => void
  select: (id: string | null) => void
  takeHistorySnapshot: () => void
  setReplayIndex: (i: number | null) => void
  clearBuffer: () => void
}

let socket: Socket | null = null
let rawWS: WebSocket | null = null

function handleEvent(event: string, data: any, set: any, get: any) {
  if (event === 'cluster:topology') {
    set({ topology: data as ClusterTopology })
  } else if (event === 'traffic:entry') {
    if (!get().capturing) return
    if (get().replayIndex !== null) return
    set((state: TrafficState) => {
      const next = [data as TrafficEntry, ...state.entries]
      if (next.length > MAX_BUFFER) next.length = MAX_BUFFER
      return { entries: next }
    })
  }
}

export const useTrafficStore = create<TrafficState>((set, get) => ({
  entries: [],
  topology: null,
  connected: false,
  capturing: true,
  selectedId: null,
  historySnapshot: null,
  replayIndex: null,

  connect: () => {
    if (BACKEND === 'hub') {
      // Connect to the Go hub via raw WebSocket.
      // The hub emits messages with {event, data} envelope.
      if (rawWS) return
      try {
        rawWS = new WebSocket(HUB_WS_URL)
      } catch (e) {
        console.error('[bullseye] WebSocket init failed', e)
        return
      }
      rawWS.onopen = () => set({ connected: true })
      rawWS.onclose = () => set({ connected: false })
      rawWS.onerror = (err) => console.error('[bullseye] ws error', err)
      rawWS.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data)
          handleEvent(msg.event, msg.data, set, get)
        } catch (e) {
          console.error('[bullseye] ws parse error', e)
        }
      }
      return
    }

    // BACKEND === 'simulator' — connect to the TS mini-service via socket.io
    if (socket) return
    try {
      socket = io(`/?XTransformPort=${SIM_PORT}`, {
        transports: ['polling', 'websocket'],
        upgrade: true,
        forceNew: true,
        reconnection: true,
        reconnectionAttempts: 10,
        reconnectionDelay: 1000,
        timeout: 10000,
      })
    } catch (e) {
      console.error('[bullseye] socket.io init failed', e)
      return
    }

    socket.on('connect', () => set({ connected: true }))
    socket.on('disconnect', () => set({ connected: false }))
    socket.on('connect_error', (err: any) => console.error('[bullseye] connect_error', err.message || err))
    socket.on('cluster:topology', (topo: ClusterTopology) => handleEvent('cluster:topology', topo, set, get))
    socket.on('traffic:entry', (entry: TrafficEntry) => handleEvent('traffic:entry', entry, set, get))
  },

  disconnect: () => {
    if (rawWS) { rawWS.close(); rawWS = null }
    if (socket) { socket.disconnect(); socket = null }
    set({ connected: false })
  },

  setCapturing: (on: boolean) => set({ capturing: on }),
  select: (id: string | null) => set({ selectedId: id }),

  takeHistorySnapshot: () => {
    // snapshot the current buffer in chronological order
    const sorted = [...get().entries].sort((a, b) => a.timestamp - b.timestamp)
    set({ historySnapshot: sorted, replayIndex: null })
  },

  setReplayIndex: (i: number | null) => set({ replayIndex: i }),

  clearBuffer: () => set({ entries: [], selectedId: null }),
}))

// helper to get a specific entry from the live or replay buffer
export function getEntryById(state: TrafficState, id: string): TrafficEntry | undefined {
  if (state.replayIndex !== null && state.historySnapshot) {
    return state.historySnapshot.find(e => e.id === id)
  }
  return state.entries.find(e => e.id === id)
}
