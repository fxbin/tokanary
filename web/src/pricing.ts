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

export * from './range'
