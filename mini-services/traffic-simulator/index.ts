// Bullseye traffic simulator — synthetic L7 K8s traffic streamer
// Streams HTTP / gRPC / DNS / Kafka / Redis request-response pairs that look
// like what Bullseye's real sniffer would emit from a cluster.
import { createServer } from 'http'
import { Server } from 'socket.io'

const httpServer = createServer()
const io = new Server(httpServer, {
  path: '/',
  cors: { origin: '*', methods: ['GET', 'POST'] },
  pingTimeout: 60000,
  pingInterval: 25000,
})

// ---------- cluster topology ----------
type Svc = {
  name: string
  ns: string
  kind: 'deployment' | 'statefulset' | 'external'
  ip: string
  port: number
  protocol: 'HTTP' | 'gRPC' | 'DNS' | 'Kafka' | 'Redis' | 'Postgres'
}

const services: Svc[] = [
  { name: 'ingress-nginx-controller', ns: 'ingress-nginx', kind: 'deployment', ip: '10.0.1.12', port: 443, protocol: 'HTTP' },
  { name: 'checkout-api', ns: 'checkout', kind: 'deployment', ip: '10.0.2.21', port: 8080, protocol: 'HTTP' },
  { name: 'payments-api', ns: 'payments', kind: 'deployment', ip: '10.0.3.45', port: 8080, protocol: 'HTTP' },
  { name: 'payments-worker', ns: 'payments', kind: 'deployment', ip: '10.0.3.47', port: 9090, protocol: 'gRPC' },
  { name: 'catalog-api', ns: 'catalog', kind: 'deployment', ip: '10.0.4.11', port: 8080, protocol: 'HTTP' },
  { name: 'users-api', ns: 'users', kind: 'deployment', ip: '10.0.5.30', port: 8080, protocol: 'HTTP' },
  { name: 'shipping-svc', ns: 'shipping', kind: 'deployment', ip: '10.0.6.18', port: 8080, protocol: 'HTTP' },
  { name: 'cart-svc', ns: 'checkout', kind: 'deployment', ip: '10.0.2.55', port: 8080, protocol: 'HTTP' },
  { name: 'analytics-aggregator', ns: 'analytics', kind: 'deployment', ip: '10.0.7.4', port: 9090, protocol: 'gRPC' },
  { name: 'rating-svc', ns: 'catalog', kind: 'deployment', ip: '10.0.4.40', port: 8080, protocol: 'HTTP' },
  { name: 'redis-cart', ns: 'checkout', kind: 'statefulset', ip: '10.0.2.99', port: 6379, protocol: 'Redis' },
  { name: 'postgres-payments', ns: 'payments', kind: 'statefulset', ip: '10.0.3.200', port: 5432, protocol: 'Postgres' },
  { name: 'kafka-broker', ns: 'analytics', kind: 'statefulset', ip: '10.0.7.50', port: 9092, protocol: 'Kafka' },
  { name: 'kube-dns', ns: 'kube-system', kind: 'deployment', ip: '10.0.0.10', port: 53, protocol: 'DNS' },
  { name: 'external-stripe', ns: 'external', kind: 'external', ip: '3.121.10.5', port: 443, protocol: 'HTTP' },
  { name: 'external-ups', ns: 'external', kind: 'external', ip: '5.10.22.7', port: 443, protocol: 'HTTP' },
]

const svcByName = (name: string) => services.find(s => s.name === name)!

// edges define who calls whom — used to make traffic realistic
type Edge = {
  src: string
  dst: string
  weight: number  // relative frequency
  // protocol-specific generator config
  proto: 'HTTP' | 'gRPC' | 'DNS' | 'Kafka' | 'Redis'
  paths?: { method: string; path: string; status: number[]; weights?: number[] }[]
  grpcMethod?: string
  redisCmd?: string[]
  kafkaTopic?: string
  failureRate?: number  // 0..1
}

const edges: Edge[] = [
  // ingress → checkout (web traffic)
  {
    src: 'ingress-nginx-controller',
    dst: 'checkout-api',
    weight: 25,
    proto: 'HTTP',
    paths: [
      { method: 'GET', path: '/api/checkout/cart', status: [200, 200, 200, 401, 500] },
      { method: 'POST', path: '/api/checkout/order', status: [200, 200, 201, 422, 503] },
      { method: 'GET', path: '/api/checkout/orders/{id}', status: [200, 200, 404] },
      { method: 'POST', path: '/api/checkout/ coupon', status: [200, 400] },
    ],
  },
  // ingress → catalog
  {
    src: 'ingress-nginx-controller',
    dst: 'catalog-api',
    weight: 30,
    proto: 'HTTP',
    paths: [
      { method: 'GET', path: '/api/catalog/products', status: [200, 200, 200, 503] },
      { method: 'GET', path: '/api/catalog/products/{id}', status: [200, 200, 404] },
      { method: 'GET', path: '/api/catalog/search?q=shoe', status: [200, 200, 500] },
    ],
  },
  // ingress → users
  {
    src: 'ingress-nginx-controller',
    dst: 'users-api',
    weight: 18,
    proto: 'HTTP',
    paths: [
      { method: 'POST', path: '/api/users/login', status: [200, 200, 401, 500] },
      { method: 'GET', path: '/api/users/me', status: [200, 200, 401] },
      { method: 'POST', path: '/api/users/refresh', status: [200, 401] },
    ],
  },
  // checkout → payments
  {
    src: 'checkout-api',
    dst: 'payments-api',
    weight: 14,
    proto: 'HTTP',
    failureRate: 0.05,
    paths: [
      { method: 'POST', path: '/v1/charges', status: [200, 200, 201, 402, 500, 503] },
      { method: 'GET', path: '/v1/charges/{id}', status: [200, 404] },
      { method: 'POST', path: '/v1/refunds', status: [200, 422] },
    ],
  },
  // checkout → cart
  {
    src: 'checkout-api',
    dst: 'cart-svc',
    weight: 12,
    proto: 'HTTP',
    paths: [
      { method: 'GET', path: '/cart/{user_id}', status: [200, 200, 404] },
      { method: 'POST', path: '/cart/{user_id}/items', status: [200, 201, 422] },
      { method: 'DELETE', path: '/cart/{user_id}/items/{sku}', status: [204, 404] },
    ],
  },
  // checkout → shipping
  {
    src: 'checkout-api',
    dst: 'shipping-svc',
    weight: 8,
    proto: 'HTTP',
    paths: [
      { method: 'POST', path: '/v1/shipments', status: [200, 201, 503] },
      { method: 'GET', path: '/v1/shipments/{id}/track', status: [200, 404] },
    ],
  },
  // catalog → rating
  {
    src: 'catalog-api',
    dst: 'rating-svc',
    weight: 10,
    proto: 'HTTP',
    paths: [
      { method: 'GET', path: '/ratings/{product_id}', status: [200, 200, 500] },
      { method: 'POST', path: '/ratings/{product_id}', status: [200, 201, 429] },
    ],
  },
  // payments → postgres
  {
    src: 'payments-api',
    dst: 'postgres-payments',
    weight: 15,
    proto: 'HTTP',
    paths: [
      { method: 'SELECT', path: 'payments WHERE id=$1', status: [200, 200, 404] },
      { method: 'INSERT', path: 'charges (id,amount,currency)', status: [201, 500] },
    ],
  },
  // payments → stripe (external, TLS)
  {
    src: 'payments-api',
    dst: 'external-stripe',
    weight: 6,
    proto: 'HTTP',
    failureRate: 0.04,
    paths: [
      { method: 'POST', path: '/v1/charges', status: [200, 200, 402, 502] },
      { method: 'GET', path: '/v1/charges/{id}', status: [200, 404] },
    ],
  },
  // cart → redis
  {
    src: 'cart-svc',
    dst: 'redis-cart',
    weight: 20,
    proto: 'Redis',
    redisCmd: ['GET cart:{user}', 'SET cart:{user}', 'HGETALL cart:{user}', 'EXPIRE cart:{user} 900', 'DEL cart:{user}'],
  },
  // users → kafka
  {
    src: 'users-api',
    dst: 'kafka-broker',
    weight: 5,
    proto: 'Kafka',
    kafkaTopic: 'user-events',
  },
  // analytics consumes kafka
  {
    src: 'analytics-aggregator',
    dst: 'kafka-broker',
    weight: 4,
    proto: 'Kafka',
    kafkaTopic: 'user-events',
  },
  // payments internal grpc
  {
    src: 'payments-api',
    dst: 'payments-worker',
    weight: 7,
    proto: 'gRPC',
    grpcMethod: 'payments.v1.Worker/ProcessRefund',
  },
  // DNS — every pod resolves service names
  {
    src: 'checkout-api',
    dst: 'kube-dns',
    weight: 9,
    proto: 'DNS',
  },
  {
    src: 'payments-api',
    dst: 'kube-dns',
    weight: 8,
    proto: 'DNS',
  },
  {
    src: 'catalog-api',
    dst: 'kube-dns',
    weight: 9,
    proto: 'DNS',
  },
  {
    src: 'users-api',
    dst: 'kube-dns',
    weight: 7,
    proto: 'DNS',
  },
]

// ---------- helpers ----------
let counter = 0
const uid = () => `pkt_${Date.now().toString(36)}_${(counter++).toString(36)}`
const pick = <T,>(arr: T[]): T => arr[Math.floor(Math.random() * arr.length)]
const rand = (min: number, max: number) => Math.floor(min + Math.random() * (max - min))
const podSuffix = () => `${rand(0, 9)}-${rand(1000, 9999).toString(16).toLowerCase()}`
const userIds = ['u_8821', 'u_4419', 'u_9930', 'u_1207', 'u_5562', 'u_7781']
const productIds = ['p_sku_001', 'p_sku_042', 'p_sku_118', 'p_sku_555', 'p_sku_902']
const orderIds = ['ord_550e8400', 'ord_6ba0b4ff', 'ord_1f0a2c33', 'ord_88d3e441']

const fillTemplate = (s: string): string =>
  s
    .replace('{user_id}', pick(userIds))
    .replace('{user}', pick(userIds))
    .replace('{product_id}', pick(productIds))
    .replace('{sku}', pick(productIds))
    .replace('{id}', pick([...orderIds, ...productIds]))
    .replace('{q=shoe}', 'shoe')

// weight-bucket the edges for fast random selection
const totalWeight = edges.reduce((s, e) => s + e.weight, 0)
function pickEdge(): Edge {
  let r = Math.random() * totalWeight
  for (const e of edges) {
    r -= e.weight
    if (r <= 0) return e
  }
  return edges[edges.length - 1]
}

// retry-state — occasionally inject retry storms (same request repeated)
let stormActiveUntil = 0
let stormEdge: Edge | null = null
let stormPath = ''

function maybeStartStorm(now: number) {
  if (now > stormActiveUntil && Math.random() < 0.012) {
    // start a retry storm lasting 8-15 seconds
    stormActiveUntil = now + rand(8000, 15000)
    stormEdge = edges.find(e => e.proto === 'HTTP' && e.dst === 'payments-api') || null
    stormPath = 'POST /v1/charges'
  }
}

// ---------- entry generation ----------
function genEntry(now: number) {
  maybeStartStorm(now)
  let edge: Edge
  let forceStatus: number | null = null
  let requestId: string | null = null
  let isRetry = false

  if (stormEdge && now < stormActiveUntil && Math.random() < 0.6) {
    edge = stormEdge
    stormPath = 'POST /v1/charges'
    requestId = `req_storm_${Math.floor(now / 1000)}`
    forceStatus = pick([503, 503, 502, 500, 504])
    isRetry = true
  } else {
    edge = pickEdge()
  }

  const src = svcByName(edge.src)
  const dst = svcByName(edge.dst)

  const srcPod = `${src.name}-${podSuffix()}`
  const dstPod = `${dst.name}-${podSuffix()}`

  const entry: any = {
    id: uid(),
    timestamp: now,
    protocol: edge.proto,
    tls: dst.kind === 'external' || src.ns === 'ingress-nginx' && dst.port === 443,
    source: { ns: src.ns, pod: srcPod, service: src.name, ip: src.ip, port: src.port },
    destination: { ns: dst.ns, pod: dstPod, service: dst.name, ip: dst.ip, port: dst.port },
    requestId: requestId || undefined,
    isRetry,
  }

  if (edge.proto === 'HTTP') {
    const pathConfig = edge.paths ? pick(edge.paths) : { method: 'GET', path: '/', status: [200] }
    const method = pathConfig.method
    const path = fillTemplate(pathConfig.path)
    const status = forceStatus ?? pick(pathConfig.status)
    const latency = status >= 500 ? rand(800, 3500) : rand(8, 220)
    entry.method = method
    entry.path = path
    entry.status = status
    entry.latencyMs = latency
    entry.request = {
      headers: {
        'content-type': 'application/json',
        'user-agent': `${src.name}/v1.4.2 (k8s)`,
        'x-request-id': requestId || `req_${Math.random().toString(36).slice(2, 10)}`,
        'x-b3-traceid': Math.random().toString(16).slice(2, 18),
        authorization: 'Bearer eyJhbGciOi***.eyJzdWIiOiJ1c***.SflKxwRJSMeKKF2QT4f',
      },
      body: method === 'GET' || method === 'DELETE' ? null : JSON.stringify(sampleBody(edge.dst, path), null, 2),
      size: rand(80, 1200),
    }
    entry.response = {
      headers: {
        'content-type': status >= 500 ? 'application/json' : 'application/json',
        'x-request-id': entry.request.headers['x-request-id'],
        date: new Date(now).toUTCString(),
      },
      body: JSON.stringify(sampleResponseBody(status, edge.dst, path), null, 2),
      size: rand(120, 2400),
    }
  } else if (edge.proto === 'gRPC') {
    const status = forceStatus ?? pick([0, 0, 0, 0, 13, 14, 2])  // 0 = OK
    entry.method = edge.grpcMethod || 'app.v1.Service/Method'
    entry.path = ''
    entry.status = status  // gRPC status code
    entry.grpcStatus = grpcStatusName(status)
    entry.latencyMs = status === 0 ? rand(4, 60) : rand(200, 1200)
    entry.request = {
      headers: { 'content-type': 'application/grpc', 'grpc-encoding': 'identity', 'grpc-trace-bin': '...' },
      body: JSON.stringify({ refund_id: pick(orderIds), amount_cents: rand(100, 9900), reason: 'customer_request' }, null, 2),
      size: rand(40, 320),
    }
    entry.response = {
      headers: { 'content-type': 'application/grpc', 'grpc-status': String(status) },
      body: status === 0 ? JSON.stringify({ ok: true, processed_at: now }, null, 2) : JSON.stringify({ error: grpcStatusName(status), detail: 'downstream timeout' }, null, 2),
      size: rand(20, 200),
    }
  } else if (edge.proto === 'Redis') {
    const cmd = edge.redisCmd ? fillTemplate(pick(edge.redisCmd)) : 'GET cart:u_8821'
    const isWrite = cmd.startsWith('SET') || cmd.startsWith('DEL') || cmd.startsWith('EXPIRE') || cmd.startsWith('HSET')
    entry.method = cmd.split(' ')[0]
    entry.path = cmd.split(' ').slice(1).join(' ')
    entry.status = 200
    entry.latencyMs = rand(1, 12)
    entry.request = { headers: {}, body: cmd, size: cmd.length }
    entry.response = {
      headers: {},
      body: isWrite ? 'OK' : pick(['"cart:u_8821"', '(nil)', '"5"', '"p_sku_042,p_sku_118"']),
      size: rand(4, 60),
    }
  } else if (edge.proto === 'Kafka') {
    const status = forceStatus ?? 200
    entry.method = 'PRODUCE'
    if (src.name === 'analytics-aggregator') entry.method = 'CONSUME'
    entry.path = `topic=${edge.kafkaTopic}`
    entry.status = status
    entry.latencyMs = rand(3, 40)
    entry.request = {
      headers: { 'correlation-id': `k_${rand(1000, 9999)}` },
      body: JSON.stringify({
        topic: edge.kafkaTopic,
        partition: rand(0, 3),
        offset: rand(10000, 99999),
        key: pick(userIds),
        event_type: entry.method === 'PRODUCE' ? pick(['user.login', 'user.signup', 'user.logout']) : 'fetch',
      }, null, 2),
      size: rand(60, 400),
    }
    entry.response = {
      headers: {},
      body: JSON.stringify({ topic: edge.kafkaTopic, partition: rand(0, 3), offset: rand(10000, 99999), timestamp: now }, null, 2),
      size: rand(30, 100),
    }
  } else if (edge.proto === 'DNS') {
    // DNS — query service names
    const queries = [
      'payments-api.payments.svc.cluster.local',
      'checkout-api.checkout.svc.cluster.local',
      'catalog-api.catalog.svc.cluster.local',
      'redis-cart.checkout.svc.cluster.local',
      'kafka-broker.analytics.svc.cluster.local',
      'postgres-payments.payments.svc.cluster.local',
      'rating-svc.catalog.svc.cluster.local',
      'shipping-svc.shipping.svc.cluster.local',
      'external-stripe.external.svc.cluster.local',
    ]
    const query = pick(queries)
    const isNx = Math.random() < 0.08
    entry.dnsQuery = query
    entry.dnsType = 'A'
    entry.dnsRcode = isNx ? 'NXDOMAIN' : 'NOERROR'
    entry.dnsAnswer = isNx ? [] : [`10.0.${rand(2, 7)}.${rand(2, 250)}`]
    entry.dnsTtl = isNx ? 0 : rand(5, 30)
    entry.latencyMs = rand(1, 8)
    entry.method = 'QUERY'
    entry.path = query
    entry.status = isNx ? 3 : 0
    entry.request = { headers: {}, body: `QUERY ${query} A`, size: query.length + 8 }
    entry.response = {
      headers: {},
      body: isNx ? `${query} A NXDOMAIN` : `${query} A ${entry.dnsAnswer[0]} (ttl ${entry.dnsTtl})`,
      size: rand(20, 80),
    }
  }

  return entry
}

function grpcStatusName(code: number): string {
  const m: Record<number, string> = {
    0: 'OK', 1: 'CANCELLED', 2: 'UNKNOWN', 3: 'INVALID_ARGUMENT',
    4: 'DEADLINE_EXCEEDED', 5: 'NOT_FOUND', 6: 'ALREADY_EXISTS', 7: 'PERMISSION_DENIED',
    8: 'RESOURCE_EXHAUSTED', 9: 'FAILED_PRECONDITION', 10: 'ABORTED', 11: 'OUT_OF_RANGE',
    12: 'UNIMPLEMENTED', 13: 'INTERNAL', 14: 'UNAVAILABLE', 15: 'DATA_LOSS', 16: 'UNAUTHENTICATED',
  }
  return m[code] || 'UNKNOWN'
}

function sampleBody(dst: string, path: string): any {
  if (path.includes('/login')) return { email: pick(['alice@shop.io', 'bob@shop.io']), password: '********' }
  if (path.includes('/order')) return { user_id: pick(userIds), items: [{ sku: pick(productIds), qty: rand(1, 3) }], total_cents: rand(2000, 19999) }
  if (path.includes('/charges')) return { amount: rand(100, 9999), currency: 'usd', source: 'tok_visa', description: 'order payment' }
  if (path.includes('/shipments')) return { order_id: pick(orderIds), address: '221B Baker St, London' }
  if (path.includes('/ratings')) return { product_id: pick(productIds), stars: rand(1, 5), comment: 'looks great' }
  if (path.includes('/items')) return { sku: pick(productIds), qty: rand(1, 2) }
  return { ok: true }
}

function sampleResponseBody(status: number, dst: string, path: string): any {
  if (status >= 500) return { error: 'internal_server_error', message: 'upstream timeout', request_id: `req_${Math.random().toString(36).slice(2, 10)}` }
  if (status === 503) return { error: 'service_unavailable', message: 'circuit breaker open' }
  if (status === 502) return { error: 'bad_gateway', message: 'upstream connect error' }
  if (status === 429) return { error: 'rate_limited', retry_after: 30 }
  if (status === 402) return { error: 'card_declined', code: 'card_declined', decline_code: 'insufficient_funds' }
  if (status === 401) return { error: 'unauthorized', message: 'token expired' }
  if (status === 404) return { error: 'not_found' }
  if (status === 422) return { error: 'validation_failed', fields: ['items'] }
  if (path.includes('/login')) return { token: 'eyJhbGciOiJIU***', user_id: pick(userIds), expires_in: 3600 }
  if (path.includes('/charges')) return { id: `ch_${Math.random().toString(36).slice(2, 12)}`, amount: rand(100, 9999), status: 'succeeded', captured: true }
  if (path.includes('/products')) return { id: pick(productIds), name: 'Wireless Headphones', price_cents: rand(2999, 29999), stock: rand(0, 100) }
  if (path.includes('/cart')) return { user_id: pick(userIds), items: [{ sku: pick(productIds), qty: rand(1, 3) }] }
  if (path.includes('/orders')) return { id: pick(orderIds), status: 'confirmed', total_cents: rand(2000, 19999) }
  if (path.includes('/ratings')) return { product_id: pick(productIds), avg: 4.3, count: 128 }
  return { ok: true, ts: Date.now() }
}

// ---------- streaming ----------
io.on('connection', (socket) => {
  console.log(`[bullseye-sim] client connected: ${socket.id}`)

  // immediately send the cluster topology so the UI can render
  socket.emit('cluster:topology', {
    services: services.map(s => ({
      name: s.name, ns: s.ns, kind: s.kind, ip: s.ip, port: s.port, protocol: s.protocol,
    })),
    edges: edges.map(e => ({ src: e.src, dst: e.dst, proto: e.proto, weight: e.weight })),
  })

  // immediately send a 200-entry backlog so the UI isn't empty
  const now = Date.now()
  for (let i = 0; i < 200; i++) {
    socket.emit('traffic:entry', genEntry(now - (200 - i) * 80))
  }

  // stream new entries every ~120ms (~8/sec)
  const interval = setInterval(() => {
    // sometimes burst 2-3 entries (realistic batching)
    const n = Math.random() < 0.3 ? 2 : 1
    for (let i = 0; i < n; i++) {
      socket.emit('traffic:entry', genEntry(Date.now()))
    }
  }, 220)

  socket.on('filter:set', (filter: string) => {
    // server doesn't filter — client does. ack for UX.
    socket.emit('filter:ack', { ok: true, filter, at: Date.now() })
  })

  socket.on('disconnect', () => {
    console.log(`[bullseye-sim] client disconnected: ${socket.id}`)
    clearInterval(interval)
  })

  socket.on('error', (e) => console.error('socket error', e))
})

const PORT = 3030
httpServer.listen(PORT, () => {
  console.log(`[bullseye-sim] streaming synthetic K8s traffic on port ${PORT}`)
})

process.on('SIGTERM', () => httpServer.close(() => process.exit(0)))
process.on('SIGINT', () => httpServer.close(() => process.exit(0)))

process.on('uncaughtException', (err) => {
  console.error('[bullseye-sim] uncaughtException:', err)
})
process.on('unhandledRejection', (err) => {
  console.error('[bullseye-sim] unhandledRejection:', err)
})

// keep process alive — log heartbeat every 30s
setInterval(() => {
  console.log(`[bullseye-sim] heartbeat — port ${PORT}, uptime ${process.uptime().toFixed(0)}s`)
}, 30000)
