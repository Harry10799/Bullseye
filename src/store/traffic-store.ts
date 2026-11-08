'use client'

import { create } from 'zustand'
import { io, Socket } from 'socket.io-client'
import type { TrafficEntry, ClusterTopology } from '@/lib/traffic-types'

const MAX_BUFFER = 1500  // ring buffer size
const WS_PORT = 3033  // simulator mini-service port — set below to match the actual mini-service

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

// NOTE: the simulator runs on port 3030 — keep this in sync with mini-services/traffic-simulator/index.ts
const SIM_PORT = 3030

export const useTrafficStore = create<TrafficState>((set, get) => ({
  entries: [],
  topology: null,
  connected: false,
  capturing: true,
  selectedId: null,
  historySnapshot: null,
  replayIndex: null,

  connect: () => {
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

    socket.on('cluster:topology', (topo: ClusterTopology) => {
      set({ topology: topo })
    })

    socket.on('traffic:entry', (entry: TrafficEntry) => {
      if (!get().capturing) return
      // skip new entries during replay
      if (get().replayIndex !== null) return
      set(state => {
        const next = [entry, ...state.entries]
        if (next.length > MAX_BUFFER) next.length = MAX_BUFFER
        return { entries: next }
      })
    })
  },

  disconnect: () => {
    socket?.disconnect()
    socket = null
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
