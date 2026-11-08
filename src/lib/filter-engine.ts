// Bullseye filter DSL — Wireshark-like display filter for K8s L7 traffic.
//
// Grammar (case-insensitive keywords):
//   expr   := orExpr
//   orExpr := andExpr (OR andExpr)*
//   andExpr:= notExpr (AND notExpr)*
//   notExpr:= NOT notExpr | atom
//   atom   := '(' expr ')' | comparison
//   comparison := field OP value
//   field  := identifier ('.' identifier)?
//   OP     := == | != | >= | <= | > | < | contains | =~ | !~=
//   value  := number | quotedString | bareword
//
// Field registry (resolved against TrafficEntry):
//   protocol         — HTTP | gRPC | DNS | Kafka | Redis | Postgres
//   method           — HTTP method or redis cmd or kafka produce/consume
//   path             — request path / topic / redis key
//   status           — numeric status code
//   latency          — numeric, ms
//   tls              — bool
//   src.pod          — source pod name
//   src.svc          — source service name
//   src.ns           — source namespace
//   src.ip           — source IP
//   dst.pod | dst.svc | dst.ns | dst.ip
//   ns               — either source or destination namespace (helper)
//   pod              — either source or destination pod
//   svc              — either source or destination service
//   dns.query        — DNS query name
//   dns.rcode        — NOERROR | NXDOMAIN
//   request.body     — substring search
//   response.body    — substring search
//   request.header.X — request header X (lowercased)
//   response.header.X
//   request.id       — requestId
//
// Examples:
//   src.pod == "payments-api*" and status >= 500
//   protocol == "HTTP" and path contains "/v1/charges"
//   dns.rcode == "NXDOMAIN"
//   latency > 1000

import type { TrafficEntry } from './traffic-types'

export type FilterAst =
  | { kind: 'and'; left: FilterAst; right: FilterAst }
  | { kind: 'or'; left: FilterAst; right: FilterAst }
  | { kind: 'not'; operand: FilterAst }
  | { kind: 'cmp'; field: string; op: string; value: string | number; raw: string }

export interface FilterResult {
  ok: boolean
  ast?: FilterAst
  error?: string
}

// --- tokenizer ---
type Token =
  | { t: 'lparen' }
  | { t: 'rparen' }
  | { t: 'and' }
  | { t: 'or' }
  | { t: 'not' }
  | { t: 'op'; v: string }
  | { t: 'field'; v: string }
  | { t: 'value'; v: string; isNum: boolean }

const OPS = ['==', '!=', '>=', '<=', '>', '<', 'contains', '=~', '!=~']

function tokenize(input: string): Token[] {
  const tokens: Token[] = []
  let i = 0
  const s = input.trim()
  while (i < s.length) {
    // skip whitespace
    while (i < s.length && /\s/.test(s[i])) i++
    if (i >= s.length) break
    const c = s[i]
    if (c === '(') { tokens.push({ t: 'lparen' }); i++; continue }
    if (c === ')') { tokens.push({ t: 'rparen' }); i++; continue }
    // string literal
    if (c === '"' || c === "'") {
      const quote = c
      i++
      let buf = ''
      while (i < s.length && s[i] !== quote) {
        if (s[i] === '\\' && i + 1 < s.length) { buf += s[i + 1]; i += 2; continue }
        buf += s[i]; i++
      }
      i++ // skip closing quote
      tokens.push({ t: 'value', v: buf, isNum: false })
      continue
    }
    // identifier / keyword / number / operator (longest match)
    // try 2-char operators first
    const two = s.slice(i, i + 2)
    if (two === '==' || two === '!=' || two === '>=' || two === '<=') {
      tokens.push({ t: 'op', v: two }); i += 2; continue
    }
    // word — collect alphanumeric . _ - * ? /
    let word = ''
    while (i < s.length && /[A-Za-z0-9._\-*?/]/.test(s[i])) {
      word += s[i]; i++
    }
    if (word.length === 0) {
      // single-char operators
      if (c === '>' || c === '<') { tokens.push({ t: 'op', v: c }); i++; continue }
      throw new FilterError(`Unexpected char '${c}' at position ${i}`)
    }
    const lower = word.toLowerCase()
    if (lower === 'and' || lower === '&&') { tokens.push({ t: 'and' }); continue }
    if (lower === 'or' || lower === '||') { tokens.push({ t: 'or' }); continue }
    if (lower === 'not' || lower === '!') { tokens.push({ t: 'not' }); continue }
    if (lower === 'contains') { tokens.push({ t: 'op', v: 'contains' }); continue }
    if (lower === '=~' || lower === 'matches') { tokens.push({ t: 'op', v: '=~' }); continue }
    // is it a number?
    if (/^-?\d+(\.\d+)?$/.test(word)) {
      tokens.push({ t: 'value', v: word, isNum: true }); continue
    }
    // bareword that looks like an operator? no — treat as field or value
    // heuristic: if next non-space token is an OP, this is a field
    tokens.push({ t: 'field', v: word })
  }
  // After tokenizing, bareword values (after an OP) — we tagged them as 'field'.
  // Fix in parser by checking context.
  return tokens
}

class FilterError extends Error {}

// --- parser (recursive descent) ---
function parse(tokens: Token[]): FilterAst {
  let pos = 0
  const peek = () => tokens[pos]
  const eat = () => tokens[pos++]
  const expect = (t: string) => {
    const tk = eat()
    if (!tk || tk.t !== t) throw new FilterError(`Expected ${t}, got ${tk ? tk.t : 'end'}`)
    return tk
  }

  function parseOr(): FilterAst {
    let left = parseAnd()
    while (peek() && peek().t === 'or') {
      eat()
      const right = parseAnd()
      left = { kind: 'or', left, right }
    }
    return left
  }

  function parseAnd(): FilterAst {
    let left = parseNot()
    while (peek() && peek().t === 'and') {
      eat()
      const right = parseNot()
      left = { kind: 'and', left, right }
    }
    return left
  }

  function parseNot(): FilterAst {
    if (peek() && peek().t === 'not') {
      eat()
      return { kind: 'not', operand: parseNot() }
    }
    return parseAtom()
  }

  function parseAtom(): FilterAst {
    const tk = peek()
    if (!tk) throw new FilterError('Unexpected end of input')
    if (tk.t === 'lparen') {
      eat()
      const e = parseOr()
      expect('rparen')
      return e
    }
    if (tk.t === 'field') {
      eat()
      const field = tk.v
      const opTk = peek()
      if (!opTk || opTk.t !== 'op') throw new FilterError(`Expected operator after '${field}'`)
      eat()
      const op = opTk.v
      // value — could be a 'field' token used as bareword
      const valTk = peek()
      if (!valTk) throw new FilterError(`Expected value after '${op}'`)
      if (valTk.t === 'value') {
        eat()
        return { kind: 'cmp', field, op, value: valTk.isNum ? Number(valTk.v) : valTk.v, raw: valTk.v }
      }
      if (valTk.t === 'field') {
        eat()
        return { kind: 'cmp', field, op, value: valTk.v, raw: valTk.v }
      }
      throw new FilterError(`Expected value after '${op}'`)
    }
    throw new FilterError(`Unexpected token '${tk.t}'`)
  }

  const ast = parseOr()
  if (pos < tokens.length) throw new FilterError(`Trailing tokens at position ${pos}`)
  return ast
}

// --- field resolver ---
function getField(entry: TrafficEntry, field: string): { value: any; type: 'string' | 'number' | 'bool' | 'list' } {
  const f = field.toLowerCase()
  switch (f) {
    case 'protocol': return { value: entry.protocol, type: 'string' }
    case 'method': return { value: entry.method || '', type: 'string' }
    case 'path': return { value: entry.path || '', type: 'string' }
    case 'status': return { value: entry.status ?? -1, type: 'number' }
    case 'latency': return { value: entry.latencyMs, type: 'number' }
    case 'tls': return { value: entry.tls, type: 'bool' }
    case 'grpc.status': return { value: entry.grpcStatus || '', type: 'string' }
    case 'src.pod': return { value: entry.source.pod, type: 'string' }
    case 'src.svc': return { value: entry.source.service, type: 'string' }
    case 'src.ns': return { value: entry.source.ns, type: 'string' }
    case 'src.ip': return { value: entry.source.ip, type: 'string' }
    case 'dst.pod': return { value: entry.destination.pod, type: 'string' }
    case 'dst.svc': return { value: entry.destination.service, type: 'string' }
    case 'dst.ns': return { value: entry.destination.ns, type: 'string' }
    case 'dst.ip': return { value: entry.destination.ip, type: 'string' }
    case 'ns':
      return { value: [entry.source.ns, entry.destination.ns], type: 'list' }
    case 'pod':
      return { value: [entry.source.pod, entry.destination.pod], type: 'list' }
    case 'svc':
      return { value: [entry.source.service, entry.destination.service], type: 'list' }
    case 'dns.query': return { value: entry.dnsQuery || '', type: 'string' }
    case 'dns.rcode': return { value: entry.dnsRcode || '', type: 'string' }
    case 'dns.type': return { value: entry.dnsType || '', type: 'string' }
    case 'request.body': return { value: entry.request.body || '', type: 'string' }
    case 'response.body': return { value: entry.response.body || '', type: 'string' }
    case 'request.id': return { value: entry.requestId || '', type: 'string' }
    case 'request.size': return { value: entry.request.size, type: 'number' }
    case 'response.size': return { value: entry.response.size, type: 'number' }
    default:
      // request.header.X / response.header.X
      if (f.startsWith('request.header.')) {
        const h = f.slice('request.header.'.length)
        return { value: entry.request.headers[h] || entry.request.headers[h.toLowerCase()] || '', type: 'string' }
      }
      if (f.startsWith('response.header.')) {
        const h = f.slice('response.header.'.length)
        return { value: entry.response.headers[h] || entry.response.headers[h.toLowerCase()] || '', type: 'string' }
      }
  }
  return { value: '', type: 'string' }
}

function globToRegex(g: string): RegExp {
  // support * and ?
  let r = '^'
  for (const c of g) {
    if (c === '*') r += '.*'
    else if (c === '?') r += '.'
    else if ('.+()[]{}|^$\\'.includes(c)) r += '\\' + c
    else r += c
  }
  r += '$'
  return new RegExp(r, 'i')
}

function compare(entry: TrafficEntry, ast: FilterAst): boolean {
  if (ast.kind === 'and') return compare(entry, ast.left) && compare(entry, ast.right)
  if (ast.kind === 'or') return compare(entry, ast.left) || compare(entry, ast.right)
  if (ast.kind === 'not') return !compare(entry, ast.operand)
  // cmp
  const { field, op, value } = ast
  const { value: actual, type } = getField(entry, field)

  if (type === 'list') {
    // any-of semantics for ns/pod/svc
    return (actual as string[]).some(v => {
      const subAst: FilterAst = { kind: 'cmp', field, op, value, raw: String(value) }
      // recurse with single value by faking type
      return cmpSingle(v, op, value)
    })
  }

  return cmpSingle(actual, op, value, type)
}

function cmpSingle(actual: any, op: string, value: any, type?: 'string' | 'number' | 'bool'): boolean {
  switch (op) {
    case '==':
      if (typeof value === 'string' && (value.includes('*') || value.includes('?'))) {
        return globToRegex(value).test(String(actual))
      }
      return String(actual) === String(value)
    case '!=':
      if (typeof value === 'string' && (value.includes('*') || value.includes('?'))) {
        return !globToRegex(value).test(String(actual))
      }
      return String(actual) !== String(value)
    case '>=': return Number(actual) >= Number(value)
    case '<=': return Number(actual) <= Number(value)
    case '>': return Number(actual) > Number(value)
    case '<': return Number(actual) < Number(value)
    case 'contains':
      return String(actual).toLowerCase().includes(String(value).toLowerCase())
    case '=~': {
      try { return new RegExp(String(value), 'i').test(String(actual)) } catch { return false }
    }
    default: return false
  }
}

// --- public API ---
export function parseFilter(input: string): FilterResult {
  if (!input || !input.trim()) return { ok: true, ast: undefined }
  try {
    const tokens = tokenize(input)
    if (tokens.length === 0) return { ok: true, ast: undefined }
    const ast = parse(tokens)
    return { ok: true, ast }
  } catch (e: any) {
    return { ok: false, error: e.message || 'Parse error' }
  }
}

export function applyFilter(entry: TrafficEntry, ast?: FilterAst): boolean {
  if (!ast) return true
  return compare(entry, ast)
}
