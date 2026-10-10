/* Tokanary 定价核心(TS 移植版)
 * 与仓根 app.js 的纯逻辑逐分支对齐:默认 modelsdev(Slice1)+ 自定义源优先于口径(Slice2)。
 * 计费口径: cost = cacheRead/1e6*cache_read + cacheWrite/1e6*cache_write
 *          + input/1e6*input + output/1e6*output (单价单位 USD/1M token)。
 * 本模块刻意保持框架无关(Vue 之外也可复用);状态由调用方显式传入(PricingOpts)。 */

import { CAT_COLORS } from './palette'
import { canonicalModelKey } from './modelkey'
import { filterDaysByRange, rangeAnchor, type RangeKey } from './range'

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

/* ------------------------------------------------------------ 模型 key 归一 */

// 定义在 modelkey.ts：range.ts 的本周 Top 也要用它，而 range.ts 刻意不依赖计价核心。
export { canonicalModelKey } from './modelkey'

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
/**
 * 外部工具按模型的综合单价（$/1M），供 weekTopModels 把外部侧混进同一张榜单。
 *
 * 放在这里而不是 range.ts：那个文件刻意不依赖计价核心（见文件头注释），而单价
 * 必须走 normalizeCost 的缺价策略，两个模块的边界不能为了一个调用点破掉。
 */
export function externalUnitByModel(data: any, policy: Policy): Record<string, number> {
  const out: Record<string, number> = {}
  const ext = externalSummary(data, policy)
  for (const r of ((ext as any)?.rows || []) as any[]) {
    for (const m of r.models || []) {
      // 归一化后才能和 pi 侧撞上：外部的原始 id 带 --int / azure- / provider 前缀，
      // pi 侧入库时已经剥掉了。两侧不同键，同一模型就是两行，费用各算一半。
      const id = canonicalModelKey(String(m.id || ''))
      if (id && id !== '(unknown)' && !(id in out)) out[id] = m.unit || 0
    }
  }
  return out
}

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
        // 保留具体是哪些字段走了推算。只给一个 estimated 布尔的话，调用方
        // 只能说「这行有估算」，没法回答「切策略会不会改这行的钱」。
        estimatedFields: n.estimatedFields,
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

/* ------------------------------------------------------------ 区间费用 */

/** 一个模型 key 在区间内的外部侧 token 与费用。 */
export interface ExtRangeCell {
  tokens: number
  cost: number
  /** false 表示该工具没有逐日逐模型明细，数字是按全量构成摊出来的。 */
  exact: boolean
}

export interface ExtRangeBreakdown {
  byKey: Record<string, ExtRangeCell>
  byTool: Record<string, { tokens: number; cost: number; exact: boolean }>
  byDay: Record<string, { tokens: number; cost: number; exact: boolean }>
}

/**
 * 区间内外部工具的 token 与费用。给出两份口径，因为两份的消费者不同：
 *
 *   - `byKey` 给模型视图。模型视图按 key 汇总，pi 与外部会在同一个 key 上相遇。
 *   - `byTool` 给 rangeExt。每个工具报自己那一行，跨工具的费用不能按 key 查：
 *     key 是全局的，拿它去算单个工具会把别的工具的钱算进来。
 *   - `byDay` 给总览的主图与「近 N 天费用」。主图要的是逐日序列，而逐日逐模型
 *     恰好就是逐日的精确来源。
 *
 * 有逐日逐模型明细的工具（`external.tools[].dayModel`）逐项计价，这是精确值。
 * 没有明细的（sqlite 适配器与走 driver 的 codex）退回按全量模型占比摊算，并标
 * exact=false，让调用方能说清这条数字的来历。
 *
 * 这里刻意不用「每工具综合均价 × 区间 token」：均价是按该工具**全量**构成加权的，
 * 区间内的构成一变就偏。本机实测同一区间偏 46%，而 day×model 的数据早就在
 * payload 里了。
 */
export function externalRangeByModel(
  data: any, policy: Policy, range: RangeKey, anchor?: string | null
): ExtRangeBreakdown {
  const byKey: Record<string, ExtRangeCell> = {}
  const byTool: Record<string, { tokens: number; cost: number; exact: boolean }> = {}
  const byDay: Record<string, { tokens: number; cost: number; exact: boolean }> = {}
  const dayCell = function (d: string) {
    return byDay[d] || (byDay[d] = { tokens: 0, cost: 0, exact: true })
  }
  const bump = function (key: string, tokens: number, cost: number, exact: boolean) {
    if (!key || key === '(unknown)' || tokens <= 0) return
    let e = byKey[key]
    if (!e) e = byKey[key] = { tokens: 0, cost: 0, exact: true }
    e.tokens += tokens
    e.cost += cost
    if (!exact) e.exact = false
  }
  const a = anchor || rangeAnchor(data)
  const ext = data && data.external
  const prices = (ext && ext.prices) || {}
  const inRange = function (d: any) {
    return filterDaysByRange([{ d: String(d || ''), total: 1 }], range, a).length > 0
  }

  for (const t of ((ext && ext.tools) || []) as any[]) {
    const toolId = String((t && (t.tool || t.label)) || '')
    const tool = byTool[toolId] || (byTool[toolId] = { tokens: 0, cost: 0, exact: true })
    const rows = (t && t.dayModel) || []
    // 只有当 dayModel 覆盖了这个工具的每一个有用量日，才敢当成精确值；缺一天
    // 就整体退回摊算，否则会静默少算那一天。
    let usable = rows.length > 0
    if (usable) {
      const covered = new Set<string>()
      for (const r of rows) if (r && r.d) covered.add(String(r.d))
      for (const d of (t.days || [])) {
        if (d && d.d && !covered.has(String(d.d))) { usable = false; break }
      }
    }

    if (usable) {
      for (const r of rows) {
        if (!r || !inRange(r.d)) continue
        const tokens = numOrNull(r.total) ||
          (numOrNull(r.cacheRead) || 0) + (numOrNull(r.cacheWrite) || 0) +
          (numOrNull(r.input) || 0) + (numOrNull(r.output) || 0)
        if (tokens <= 0) continue
        const key = canonicalModelKey(String(r.key || ''))
        const n = normalizeCost((prices[r.key] && prices[r.key].cost) || {}, policy)
        const c = n.ok ? costOfTokens(r, n.values) : 0
        bump(key, tokens, c, true)
        tool.tokens += tokens
        tool.cost += c
        const dc = dayCell(String(r.d))
        dc.tokens += tokens
        dc.cost += c
      }
      continue
    }

    /* 退化路径：保留工具级总额不变（与 rangeExt 同口径），只把分配从「按 token
     * 占比」换成「按费用占比」。同一个模型在区间内花的比例本来就跟着钱走，
     * 而不跟着 token 走。 */
    const tdays = filterDaysByRange(t.days || [], range, a)
    let rangeTok = 0
    for (const d of tdays) {
      rangeTok += numOrNull(d.total) ||
        (numOrNull(d.cacheRead) || 0) + (numOrNull(d.cacheWrite) || 0) +
        (numOrNull(d.input) || 0) + (numOrNull(d.output) || 0)
    }
    if (rangeTok <= 0) continue
    tool.exact = false
    // payload 里 tools[].models 是行数组（ExtModelRow），不是 external-usage.json
    // 那个以 id 为键的对象。按对象处理会读到下标 "0"，单价与 key 全部落空，
    // 于是退���路径静默产出 0。
    const rowsOf = t.models || []
    const models: Array<{ id: string; m: any }> = Array.isArray(rowsOf)
      ? rowsOf.map(function (m: any) { return { id: String((m && m.id) || ''), m: m } })
      : Object.keys(rowsOf).map(function (id: string) { return { id: id, m: rowsOf[id] } })
    let allTok = 0, allCost = 0
    const per: Array<[string, number, number]> = []
    for (const { id, m } of models) {
      const tok = (numOrNull(m.input) || 0) + (numOrNull(m.cacheRead) || 0) +
        (numOrNull(m.cacheWrite) || 0) + (numOrNull(m.output) || 0)
      const n = normalizeCost((prices[id] && prices[id].cost) || {}, policy)
      const cost = n.ok ? costOfTokens(m, n.values) : 0
      allTok += tok
      allCost += cost
      per.push([canonicalModelKey(id), tok, cost])
    }
    // 工具级均价与旧口径同式：全量费用 ÷ 全量 token。退化路径保留均价口径，
    // 是为了不改变那些拿不到 dayModel 的工具今天的数字。
    const toolUnit = allTok > 0 ? allCost / allTok : 0
    const toolCost = toolUnit * rangeTok
    tool.tokens += rangeTok
    tool.cost += toolCost
    for (const [key, tok, cost] of per) {
      const share = allCost > 0 ? cost / allCost : (allTok > 0 ? tok / allTok : 0)
      bump(key, Math.round(rangeTok * (allTok > 0 ? tok / allTok : 0)), toolCost * share, false)
    }
    // 逐日同样按均价摊，并标 exact=false，让总览知道自己这条线是摊出来的。
    for (const d of tdays) {
      const tok = numOrNull(d.total) ||
        (numOrNull(d.cacheRead) || 0) + (numOrNull(d.cacheWrite) || 0) +
        (numOrNull(d.input) || 0) + (numOrNull(d.output) || 0)
      if (tok <= 0) continue
      const dc = dayCell(String(d.d))
      dc.exact = false
      dc.tokens += tok
      dc.cost += toolUnit * tok
    }
  }
  return { byKey, byTool, byDay }
}

/* ------------------------------------------------------------ 时间范围 */

// 数据是静态快照:范围一律以 days 最大日为锚,向前取 N 个日历日。

export * from './range'
