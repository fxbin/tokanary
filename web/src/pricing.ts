/* Tokanary 定价核心(TS 移植版)
 * 与仓根 app.js 的纯逻辑逐分支对齐:默认 modelsdev(Slice1)+ 自定义源优先于口径(Slice2)。
 * 计费口径: cost = cacheRead/1e6*cache_read + cacheWrite/1e6*cache_write
 *          + input/1e6*input + output/1e6*output (单价单位 USD/1M token)。
 * 本模块刻意保持框架无关(Vue 之外也可复用);状态由调用方显式传入(PricingOpts)。 */

import { CAT_COLORS } from './palette'

export type PriceSource = 'modelsdev'
export type Policy = 'ratio10' | 'zero' | 'inputprice'
export type ResolvedSource = 'manual' | 'custom' | 'models.dev' | 'missing'

export interface Cost4 {
  input: number | null
  output: number | null
  cache_read: number | null
  cache_write: number | null
}

export interface PricingOpts {
  policy: Policy
  priceSource: PriceSource
  overrides: Record<string, any>
  customPrices: Record<string, Cost4> | null
}

export function defaultOpts(): PricingOpts {
  return { policy: 'ratio10', priceSource: 'modelsdev', overrides: {}, customPrices: null }
}

export const POLICIES: Record<Policy, { label: string; read: number; write: number }> = {
  ratio10: { label: '缓存读 = 输入价 ×0.1，缓存写 = 输入价 ×1.25（行业惯例，推荐）', read: 0.1, write: 1.25 },
  zero: { label: '缺失的缓存单价按 0 计（成本下限）', read: 0, write: 0 },
  inputprice: { label: '缺失的缓存单价按输入价计（成本上限）', read: 1, write: 1 }
}

export const SOURCES: Record<PriceSource, { label: string; short: string }> = {
  modelsdev: { label: 'models.dev 公开价', short: 'models.dev' }
}

export const CATS = [
  { k: 'cacheRead', label: '缓存读 (cache read)', color: CAT_COLORS[0], price: 'cache_read' },
  { k: 'cacheWrite', label: '缓存写 (cache write)', color: CAT_COLORS[1], price: 'cache_write' },
  { k: 'input', label: '新增输入 (input)', color: CAT_COLORS[2], price: 'input' },
  { k: 'output', label: '输出 (output)', color: CAT_COLORS[3], price: 'output' }
] as const

/* ------------------------------------------------------------ 工具 */

export function fmt(n: number): string {
  n = Number(n) || 0
  const a = Math.abs(n)
  if (a >= 1e9) return (n / 1e9).toFixed(2) + 'B'
  if (a >= 1e6) return (n / 1e6).toFixed(2) + 'M'
  if (a >= 1e3) return (n / 1e3).toFixed(1) + 'K'
  return String(Math.round(n))
}

export function money(n: number): string {
  n = Number(n) || 0
  if (n === 0) return '$0'
  if (Math.abs(n) < 0.01) return '$' + n.toFixed(4)
  if (Math.abs(n) < 1) return '$' + n.toFixed(3)
  return '$' + n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

export function pct(n: number, digits = 1): string {
  return (Number(n) || 0).toFixed(digits) + '%'
}

export function esc(s: unknown): string {
  return String(s === null || s === undefined ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;')
}

export function numOrNull(v: unknown): number | null {
  if (v === null || v === undefined || v === '' || isNaN(Number(v))) return null
  return Number(v)
}

/* ------------------------------------------------------------ 价格解析 */

/** 按缺价策略补全四个单价 */
export function normalizeCost(cost: Partial<Cost4> | null | undefined, policy: Policy) {
  const pol = POLICIES[policy] || POLICIES.ratio10
  cost = cost || {}
  const v: Cost4 = {
    input: numOrNull(cost.input),
    output: numOrNull(cost.output),
    cache_read: numOrNull(cost.cache_read),
    cache_write: numOrNull(cost.cache_write)
  }
  const est: string[] = []
  if (v.input === null) return { values: v, ok: false, estimatedFields: [] as string[] }
  if (v.cache_read === null) { v.cache_read = (v.input as number) * pol.read; est.push('cache_read') }
  if (v.cache_write === null) { v.cache_write = (v.input as number) * pol.write; est.push('cache_write') }
  if (v.output === null) { v.output = v.input; est.push('output') }
  return { values: v, ok: true, estimatedFields: est }
}

/** 归一化用户自定义价格源(纯函数)。接受 prices-raw 形态或扁平形态,无法识别返回 null。 */
export function normalizeCustomMap(json: any): Record<string, Cost4> | null {
  if (!json || typeof json !== 'object') return null
  const out: Record<string, Cost4> = {}
  const put = function (target: any, cost: any) {
    if (typeof target !== 'string' || !cost || typeof cost !== 'object') return
    const t = target.trim()
    if (!t) return
    const c: Cost4 = {
      input: numOrNull(cost.input),
      output: numOrNull(cost.output),
      cache_read: numOrNull(cost.cache_read),
      cache_write: numOrNull(cost.cache_write)
    }
    if (c.input === null || c.output === null) return
    out[t.toLowerCase()] = c
  }
  if (Object.prototype.toString.call(json.models) === '[object Array]') {
    json.models.forEach(function (m: any) { if (m) put(m.target, m.cost) })
  } else {
    Object.keys(json).forEach(function (k) {
      if (k === 'models' || k === 'fetchedAt' || k === 'source' || k === 'note') return
      put(k, json[k])
    })
  }
  return Object.keys(out).length ? out : null
}

export interface ResolvedPrice {
  values: Cost4
  source: ResolvedSource
  via: string | null
  fellBack: boolean
  estimated: boolean
  estimatedFields: string[]
  label?: string | null
  meta: any
}

/**
 * 解析某个模型的最终单价。
 * 优先级:手动覆盖 > 自定义源 > models.dev。
 */
export function resolvePrice(key: string, data: any, opts: PricingOpts): ResolvedPrice {
  const base = (data.pricing || {})[key] || null
  const ov = opts.overrides[key] || null

  if (ov) {
    let touched = false
    const ovCost: any = {}
    ;['input', 'output', 'cache_read', 'cache_write'].forEach(function (f) {
      if (ov[f] !== null && ov[f] !== undefined && ov[f] !== '') { ovCost[f] = ov[f]; touched = true }
    })
    if (touched) {
      const n = normalizeCost(ovCost, opts.policy)
      return {
        values: n.values, source: 'manual', via: 'manual', fellBack: false,
        estimated: n.estimatedFields.length > 0, estimatedFields: n.estimatedFields,
        meta: base, label: ov.label || null
      }
    }
  }

  // 自定义源:快照命中即用(低于手动覆盖,高于内置口径)
  if (opts.customPrices && typeof opts.customPrices === 'object') {
    const cc = opts.customPrices[key] || opts.customPrices[String(key).toLowerCase()]
    if (cc) {
      const nc = normalizeCost(cc, opts.policy)
      if (nc.ok) {
        return {
          values: nc.values, source: 'custom', via: 'custom', fellBack: false,
          estimated: nc.estimatedFields.length > 0, estimatedFields: nc.estimatedFields,
          meta: base, label: '自定义源'
        }
      }
    }
  }

  const mdOk = !!(base && numOrNull((base.cost || {}).input) !== null)
  if (!mdOk) {
    return {
      values: { input: null, output: null, cache_read: null, cache_write: null },
      source: 'missing', via: null, fellBack: false, estimated: false,
      estimatedFields: [], meta: base
    }
  }

  const n2 = normalizeCost(base.cost || {}, opts.policy)
  return {
    values: n2.values,
    source: 'models.dev',
    via: 'modelsdev',
    fellBack: false,
    estimated: n2.estimatedFields.length > 0,
    estimatedFields: n2.estimatedFields,
    meta: base
  }
}

/* ------------------------------------------------------------ 成本计算 */

/** 给定 token 结构 + 已归一化的单价,算钱 */
export function costOfTokens(tok: any, price: Cost4): number {
  return (tok.cacheRead || 0) / 1e6 * (price.cache_read || 0)
    + (tok.cacheWrite || 0) / 1e6 * (price.cache_write || 0)
    + (tok.input || 0) / 1e6 * (price.input || 0)
    + (tok.output || 0) / 1e6 * (price.output || 0)
}

export function modelCost(m: any, data: any, opts: PricingOpts) {
  const p = resolvePrice(m.key, data, opts)
  if (p.source === 'missing') {
    return { total: 0, parts: {} as Record<string, number>, priced: false, price: p }
  }
  const parts: Record<string, number> = {}
  let total = 0
  CATS.forEach(function (c) {
    const v = (m[c.k] || 0) / 1e6 * ((p.values as any)[c.price] || 0)
    parts[c.k] = v
    total += v
  })
  return { total, parts, priced: true, price: p }
}

/** 汇总:总额 + 每个模型 + 未定价模型清单 */
export function computeAll(data: any, opts: PricingOpts) {
  const rows = (data.models || []).map(function (m: any) {
    const c = modelCost(m, data, opts)
    return {
      m, cost: c.total, parts: c.parts, priced: c.priced, price: c.price,
      share: 0, costShare: 0,
      unit: c.priced && m.total > 0 ? c.total / m.total * 1e6 : 0   // $/1M token 综合均价
    }
  })
  rows.sort(function (a: any, b: any) { return b.m.total - a.m.total })
  const totTok = rows.reduce(function (s: number, r: any) { return s + r.m.total }, 0)
  const totCost = rows.reduce(function (s: number, r: any) { return s + r.cost }, 0)
  const unpriced = rows.filter(function (r: any) { return !r.priced && r.m.total > 0 })
  const unpricedTokens = unpriced.reduce(function (s: number, r: any) { return s + r.m.total }, 0)
  rows.forEach(function (r: any) {
    r.share = totTok ? r.m.total / totTok * 100 : 0
    r.costShare = totCost ? r.cost / totCost * 100 : 0
  })
  return {
    rows, totalTokens: totTok, totalCost: totCost,
    unpriced, unpricedTokens,
    coverage: totTok ? (totTok - unpricedTokens) / totTok * 100 : 0,
    viaGateway: 0,
    viaModelsdev: rows.filter(function (r: any) { return r.price.via === 'modelsdev' }).length,
    fellBack: rows.filter(function (r: any) { return r.price.fellBack }).length,
    categories: CATS.map(function (c) {
      return { k: c.k, label: c.label, color: c.color, value: (data.totals || {})[c.k] || 0 }
    })
  }
}

/** 两套口径的总额对比(忽略手动覆盖与自定义源,纯口径差异) */
export function compareSources(data: any, opts: PricingOpts) {
  const out: Record<string, number> = {}
  out.modelsdev = computeAll(data, { ...opts, priceSource: 'modelsdev', overrides: {}, customPrices: null }).totalCost
  out.gateway = out.modelsdev
  return out
}

/** 其他 CLI(Claude Code / Codex / OpenCode)的汇总与计费 */
export function externalSummary(data: any, policy: Policy) {
  const ext = data.external
  if (!ext || !ext.tools || !ext.tools.length) return null
  const rows = ext.tools.map(function (t: any) {
    const models = (t.models || []).map(function (m: any) {
      const p = m.price || { cost: {} }
      const n = normalizeCost(p.cost || {}, policy)
      const cost = n.ok ? costOfTokens(m, n.values) : 0
      return {
        id: m.id, tok: m, price: p, cost, priced: n.ok, estimated: n.ok && n.estimatedFields.length > 0,
        unit: m.total > 0 && n.ok ? cost / m.total * 1e6 : 0
      }
    })
    models.sort(function (a: any, b: any) { return b.cost - a.cost || b.tok.total - a.tok.total })
    const cost = models.reduce(function (s: number, x: any) { return s + x.cost }, 0)
    const unpriced = models.filter(function (x: any) { return !x.priced && x.tok.total > 0 })
    return {
      t, models, cost,
      unpricedTokens: unpriced.reduce(function (s: number, x: any) { return s + x.tok.total }, 0),
      unit: t.total > 0 ? cost / t.total * 1e6 : 0
    }
  })
  rows.sort(function (a: any, b: any) { return b.t.total - a.t.total })
  const totalTokens = rows.reduce(function (s: number, r: any) { return s + r.t.total }, 0)
  const totalCost = rows.reduce(function (s: number, r: any) { return s + r.cost }, 0)
  return {
    rows, totalTokens, totalCost,
    unpricedTokens: rows.reduce(function (s: number, r: any) { return s + r.unpricedTokens }, 0),
    sessions: ext.totals ? ext.totals.sessions : 0,
    calls: ext.totals ? ext.totals.calls : 0,
    generatedAt: ext.generatedAt
  }
}

/** 按天成本:用每个模型「$/token」均价摊算(日粒度没有缓存/输出拆分) */
export function dailyCost(data: any, summary: { rows: any[] }) {
  const unitByKey: Record<string, number> = {}
  summary.rows.forEach(function (r: any) { unitByKey[r.m.key] = r.unit })
  const byDay: Record<string, number> = {}
  ;(data.dayModel || []).forEach(function (dm: any) {
    const v = (unitByKey[dm.key] || 0) * dm.total / 1e6
    byDay[dm.d] = (byDay[dm.d] || 0) + v
  })
  return byDay
}

/* ------------------------------------------------------------ 时间范围 */

// 数据是静态快照:范围一律以 days 最大日为锚,向前取 N 个日历日。
export type RangeKey = 'today' | '7d' | '30d' | 'all'
export const RANGES: Record<RangeKey, { label: string; days: number | null }> = {
  today: { label: '今天', days: 1 },
  '7d': { label: '近 7 天', days: 7 },
  '30d': { label: '近 30 天', days: 30 },
  all: { label: '全部', days: null }
}

export function rangeAnchor(data: any): string | null {
  const ds = (data.days || []).map(function (d: any) { return d.d }).sort()
  return ds.length ? ds[ds.length - 1] : null
}

/** 返回 cutoff 日历日(YYYY-MM-DD);null 表示不过滤 */
export function rangeCutoffKey(allDays: string[], range: RangeKey, anchor?: string | null): string | null {
  if (range === 'all') return null
  const a = anchor || allDays.slice().sort().pop() || null
  if (!a) return null
  const n = RANGES[range].days || 1
  const t = new Date(a + 'T00:00:00').getTime()
  if (isNaN(t)) return null
  const cut = new Date(t - (n - 1) * 86400000)
  const pad = (x: number) => String(x).padStart(2, '0')
  return cut.getFullYear() + '-' + pad(cut.getMonth() + 1) + '-' + pad(cut.getDate())
}

export function filterDaysByRange(days: any[], range: RangeKey, anchor?: string | null): any[] {
  if (range === 'all') return days.slice()
  const keys = days.map(function (d) { return d.d })
  const cut = rangeCutoffKey(keys, range, anchor)
  if (cut === null) return days.slice()
  const a = anchor || keys.slice().sort().pop()
  return days.filter(function (d) { return d.d >= cut && d.d <= (a || cut) })
}

export interface DayFields { cacheRead: number; cacheWrite: number; input: number; output: number; total: number }

export function sumDayFields(days: any[]): DayFields {
  const f: DayFields = { cacheRead: 0, cacheWrite: 0, input: 0, output: 0, total: 0 }
  days.forEach(function (d: any) {
    const cr = d.cacheRead || 0
    const cw = d.cacheWrite || 0
    const inp = d.input || 0
    const out = d.output || 0
    f.cacheRead += cr
    f.cacheWrite += cw
    f.input += inp
    f.output += out
    // 外部工具 days 历史缺 total 字段 → 四段之和兜底(与 ext_total 同式)
    const parts = cr + cw + inp + out
    f.total += (typeof d.total === 'number' && d.total > 0) ? d.total : parts
  })
  return f
}

export interface RangeStats {
  days: any[]
  dayCount: number
  tokens: number
  cost: number
  avgCost: number
  minCost: number
  maxCost: number
  minDay: string | null
  maxDay: string | null
  cacheHitPct: number
  fields: DayFields
}

/**
 * 范围统计:token 精确求和;费用 = 日均价摊算之和(与 dailyCost 同法,随当前口径/单价实时变)。
 * 空范围返回全 0 守卫,永不 NaN。min/avg/max 取范围内活跃日费用。
 */
export function rangeStats(
  data: any, summary: { rows: any[] },
  costByDay: Record<string, number>, range: RangeKey
): RangeStats {
  const anchor = rangeAnchor(data)
  const fdays = filterDaysByRange(data.days || [], range, anchor)
  const fields = sumDayFields(fdays)
  const costs = fdays.map(function (d: any) { return { d: d.d, c: costByDay[d.d] || 0 } })
  const cost = costs.reduce(function (s, x) { return s + x.c }, 0)
  const dayCount = fdays.length
  let minCost = 0, maxCost = 0, minDay: string | null = null, maxDay: string | null = null
  if (dayCount) {
    minCost = Infinity
    maxCost = -Infinity
    costs.forEach(function (x) {
      if (x.c < minCost) { minCost = x.c; minDay = x.d }
      if (x.c > maxCost) { maxCost = x.c; maxDay = x.d }
    })
  }
  const denom = fields.cacheRead + fields.cacheWrite + fields.input
  return {
    days: fdays, dayCount,
    tokens: fields.total, cost,
    avgCost: dayCount ? cost / dayCount : 0,
    minCost, maxCost, minDay, maxDay,
    cacheHitPct: denom ? Math.round(1000.0 * fields.cacheRead / denom) / 10 : 0,
    fields
  }
}

export interface RangeExtRow {
  label: string
  fields: DayFields
  tokens: number
  cost: number
}

/** 跨工具范围汇总:token 按各工具 days 精确求和,费用按该工具综合均价摊算。 */
export function rangeExt(
  extSummary: { rows: any[] } | null, range: RangeKey, anchor?: string | null
): { rows: RangeExtRow[]; totalTokens: number; totalCost: number } {
  const out = { rows: [] as RangeExtRow[], totalTokens: 0, totalCost: 0 }
  if (!extSummary) return out
  extSummary.rows.forEach(function (r: any) {
    const fdays = filterDaysByRange(r.t.days || [], range, anchor)
    const fields = sumDayFields(fdays.map(function (d: any) {
      return {
        cacheRead: d.cacheRead, cacheWrite: d.cacheWrite,
        input: d.input, output: d.output, total: d.total
      }
    }))
    const tokens = fields.total
    const cost = (r.unit || 0) * tokens / 1e6
    out.rows.push({ label: r.t.label, fields, tokens, cost })
    out.totalTokens += tokens
    out.totalCost += cost
  })
  out.rows.sort(function (a, b) { return b.tokens - a.tokens })
  return out
}

/* ------------------------------------------------------------ streak / 本周 Top (U6) */

/** 从 anchor 日(含)向前数连续活跃日(days 里 total>0)。anchor 缺数据则从其前一天起试;空返回 0。 */
export function calcStreak(days: any[], anchor?: string | null): { streak: number; endDate: string | null } {
  const active: Record<string, true> = {}
  ;(days || []).forEach(function (d: any) {
    if (d && d.total > 0 && d.d) active[String(d.d)] = true
  })
  const keys = Object.keys(active).sort()
  if (!keys.length) return { streak: 0, endDate: null }
  let cur = anchor || keys[keys.length - 1]
  if (!active[cur]) {
    // anchor 当天无数据:若前一天有则从前一天起算;否则视为断档
    const t = new Date(cur + 'T00:00:00').getTime()
    if (isNaN(t)) return { streak: 0, endDate: null }
    const prev = new Date(t - 86400000)
    const pad = function (x: number) { return String(x).padStart(2, '0') }
    cur = prev.getFullYear() + '-' + pad(prev.getMonth() + 1) + '-' + pad(prev.getDate())
    if (!active[cur]) return { streak: 0, endDate: null }
  }
  let n = 0
  let end: string | null = null
  const t0 = new Date(cur + 'T00:00:00').getTime()
  if (isNaN(t0)) return { streak: 0, endDate: null }
  // 从 cur 向前逐日检查;endDate = 连段里最新的一天
  for (let i = 0; i < 4000; i++) {
    const t = t0 - i * 86400000
    const dt = new Date(t)
    const pad = function (x: number) { return String(x).padStart(2, '0') }
    const k = dt.getFullYear() + '-' + pad(dt.getMonth() + 1) + '-' + pad(dt.getDate())
    if (!active[k]) break
    n++
    if (i === 0) end = k
  }
  return { streak: n, endDate: end }
}

export interface WeekTopRow { key: string; token: number; cost: number; share: number }

/** 数据最大日所在 ISO 周(周一为起点)内 dayModel 聚合,按 cost 降序 Top5。无 dayModel 返回 []。 */
export function weekTopModels(data: any, summary: { rows: any[] }, anchor?: string | null): WeekTopRow[] {
  const dm = data && data.dayModel
  if (!Array.isArray(dm) || !dm.length) return []
  const a = anchor || rangeAnchor(data)
  if (!a) return []
  const t = new Date(a + 'T00:00:00').getTime()
  if (isNaN(t)) return []
  // 周一为一周起点:JS getDay() 0=Sun → 偏移 (day+6)%7
  const dow = new Date(t).getDay()
  const back = (dow + 6) % 7
  const weekStart = new Date(t - back * 86400000)
  const pad = function (x: number) { return String(x).padStart(2, '0') }
  const startKey = weekStart.getFullYear() + '-' + pad(weekStart.getMonth() + 1) + '-' + pad(weekStart.getDate())
  const unitByKey: Record<string, number> = {}
  ;(summary.rows || []).forEach(function (r: any) {
    if (r.m && r.m.key) unitByKey[r.m.key] = r.unit || 0
  })
  const agg: Record<string, { token: number; cost: number }> = {}
  dm.forEach(function (x: any) {
    if (!x || !x.d || x.d < startKey || x.d > a) return
    const key = String(x.key || '')
    if (!key) return
    let e = agg[key]
    if (!e) e = agg[key] = { token: 0, cost: 0 }
    const tok = Number(x.total) || 0
    e.token += tok
    e.cost += (unitByKey[key] || 0) * tok / 1e6
  })
  const rows: WeekTopRow[] = Object.keys(agg).map(function (key) {
    return { key, token: agg[key].token, cost: agg[key].cost, share: 0 }
  }).filter(function (r) { return r.token > 0 || r.cost > 0 })
  rows.sort(function (a2, b) { return b.cost - a2.cost || b.token - a2.token })
  const tot = rows.reduce(function (s, r) { return s + r.cost }, 0)
  rows.forEach(function (r) { r.share = tot > 0 ? r.cost / tot * 100 : 0 })
  return rows.slice(0, 5)
}

/* ------------------------------------------------------------ hours / heatmap / 洞察 (U2) */

export interface HourRow { h: string; total: number; cacheRead?: number; cacheWrite?: number; input?: number; output?: number }

/** 按 range 裁剪 hours:取 `h` 前 10 位(YYYY-MM-DD) 走与 days 同一窗口逻辑。 */
export function filterHoursByRange(hours: HourRow[], range: RangeKey, anchor?: string | null): HourRow[] {
  if (!Array.isArray(hours) || !hours.length) return []
  if (range === 'all') return hours.slice()
  const keys = hours.map(function (x) { return String(x.h || '').slice(0, 10) }).filter(Boolean)
  const cut = rangeCutoffKey(keys, range, anchor)
  if (cut === null) return hours.slice()
  const a = anchor || keys.slice().sort().pop()
  return hours.filter(function (x) {
    const d = String(x.h || '').slice(0, 10)
    return d >= cut && d <= (a || cut)
  })
}

/** weekday(0=Sun)×hour(0-23) 的 token 强度矩阵;空格 0。返回 7×24。 */
export function hourMatrix(hours: HourRow[]): number[][] {
  const m: number[][] = []
  for (let w = 0; w < 7; w++) {
    m.push(new Array(24).fill(0))
  }
  ;(hours || []).forEach(function (x) {
    const h = String(x.h || '')
    if (h.length < 13) return
    const day = h.slice(0, 10)
    const hour = Number(h.slice(11, 13))
    if (!isFinite(hour) || hour < 0 || hour > 23) return
    const t = new Date(day + 'T00:00:00')
    if (isNaN(t.getTime())) return
    const w = t.getDay()
    m[w][hour] += Number(x.total) || 0
  })
  return m
}

export interface RangeInsights {
  cacheHitPct: number
  cacheRead: number
  cacheWrite: number
  input: number
  peakDay: string | null
  peakDayCost: number
  peakModel: string | null
  peakModelCost: number
  avgBurn: number
  forecast30: number
  dayCount: number
}

/**
 * 范围洞察:缓存效率用与 rangeStats 同式(读/(读+写+新增输入));
 * 最贵日 = 范围内 costByDay 最大;当日主模型 = dayModel 在该日 total 最大者;
 * 日均 burn = rangeStats.avgCost;月末预测 = 日均×30(纯前端示意,非结算)。
 * 空范围全 0 / null 守卫,永不 NaN。
 */
export function rangeInsights(
  data: any, summary: { rows: any[] },
  costByDay: Record<string, number>, range: RangeKey
): RangeInsights {
  const rs = rangeStats(data, summary, costByDay, range)
  let peakDay: string | null = null
  let peakDayCost = 0
  rs.days.forEach(function (d) {
    const c = costByDay[d.d] || 0
    if (c > peakDayCost || (peakDay === null && c > 0)) {
      peakDayCost = c
      peakDay = d.d
    }
  })
  let peakModel: string | null = null
  let peakModelCost = 0
  if (peakDay) {
    ;(data.dayModel || []).forEach(function (dm: any) {
      if (dm.d !== peakDay) return
      const unit = (summary.rows || []).find(function (r: any) { return r.m && r.m.key === dm.key })
      const u = unit && (unit as any).unit ? (unit as any).unit : 0
      const cost = u * (dm.total || 0) / 1e6
      if (cost > peakModelCost) {
        peakModelCost = cost
        peakModel = dm.key
      }
    })
  }
  return {
    cacheHitPct: rs.cacheHitPct,
    cacheRead: rs.fields.cacheRead,
    cacheWrite: rs.fields.cacheWrite,
    input: rs.fields.input,
    peakDay,
    peakDayCost,
    peakModel,
    peakModelCost,
    avgBurn: rs.avgCost,
    forecast30: rs.avgCost * 30,
    dayCount: rs.dayCount
  }
}
