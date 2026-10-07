/* 范围聚合与时间窗 —— 与计价核心分离，纯函数、无依赖 pricing 内部。 */
export type RangeKey = 'today' | '7d' | '30d' | 'all'
export const RANGES: Record<RangeKey, { label: string; days: number | null }> = {
  today: { label: '今天', days: 1 },
  '7d': { label: '近 7 天', days: 7 },
  '30d': { label: '近 30 天', days: 30 },
  all: { label: '全部', days: null }
}

export function rangeAnchor(data: any): string | null {
  // Anchor must be the freshest day across ALL sources. pi warehouse may lag
  // behind external tools (or vice versa); using only data.days clipped every
  // newer external row out of the 7d/30d window.
  let max: string | null = null
  const take = (d: any) => {
    const k = typeof d === 'string' ? d : d && d.d
    if (typeof k === 'string' && k && (!max || k > max)) max = k
  }
  for (const d of data?.days || []) take(d)
  for (const t of data?.external?.tools || []) {
    for (const d of t.days || []) take(d)
  }
  return max
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

/**
 * 合并 pi 仓库 days 与各 external 工具 days，得到「全工具」逐日序列。
 * 主图/近 N 天 KPI 必须用这个，否则 pi 过期时窗口会整段空掉。
 */
export function mergeDailyUsage(data: any): any[] {
  const byDay: Record<string, any> = {}
  const bump = (key: string, src: any) => {
    if (!key) return
    let e = byDay[key]
    if (!e) {
      e = byDay[key] = { d: key, cacheRead: 0, cacheWrite: 0, input: 0, output: 0, total: 0, reasoning: 0 }
    }
    e.cacheRead += Number(src.cacheRead || src.cache_read || 0)
    e.cacheWrite += Number(src.cacheWrite || src.cache_write || 0)
    e.input += Number(src.input || 0)
    e.output += Number(src.output || 0)
    e.reasoning += Number(src.reasoning || 0)
    const t = Number(src.total || 0)
    e.total += t || (Number(src.input || 0) + Number(src.output || 0) + Number(src.cacheRead || src.cache_read || 0) + Number(src.cacheWrite || src.cache_write || 0))
  }
  for (const d of data?.days || []) {
    if (d && d.d) bump(String(d.d), d)
  }
  for (const t of data?.external?.tools || []) {
    for (const d of t.days || []) {
      if (d && d.d) bump(String(d.d), d)
    }
  }
  return Object.keys(byDay).sort().map((k) => byDay[k])
}

/** 合并 pi 日费用与 external 日费用（external 按该工具综合均价摊到天）。
 *  r.unit 来自 externalSummary:该工具全量费用 ÷ 全量 token × 1e6,是按真实
 *  四段 token 构成加权的综合均价,不是四档单价的算术平均。 */
export function mergeDailyCost(
  costByDay: Record<string, number>,
  extSummary: { rows: any[]; totalTokens?: number; totalCost?: number } | null,
  data: any
): Record<string, number> {
  const out: Record<string, number> = { ...costByDay }
  if (!extSummary) return out
  const tools = data?.external?.tools || []
  const rows = extSummary.rows || []
  rows.forEach((r: any, i: number) => {
    const t = tools[i]
    // match by label when possible
    const tool = tools.find((x: any) => x.label === r.label || x.tool === r.label) || t
    if (!tool) return
    const unit = r.unit || 0
    for (const d of tool.days || []) {
      if (!d || !d.d) continue
      const tok = Number(d.total || 0) ||
        Number(d.input || 0) + Number(d.output || 0) + Number(d.cacheRead || 0) + Number(d.cacheWrite || 0)
      out[d.d] = (out[d.d] || 0) + unit * tok / 1e6
    }
  })
  return out
}

/** 装配「全工具」视图：days 合并后可直接喂 rangeStats / dayChart。 */
export function withMergedDays(data: any): any {
  return { ...data, days: mergeDailyUsage(data) }
}


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

/** 跨工具范围汇总:token 按各工具 days 精确求和,费用按该工具综合均价摊算。
 *  r.unit 与 pi 侧 rows[].unit 同为「按真实 token 构成加权」的 $/1M(见
 *  mergeDailyCost 注释),不是四价算术平均 —— 缓存占大头的工具因此不会被高估。
 *  日粒度没有 per-model 明细,故按全量构成摊到范围内,与 pi 侧 dailyCost 同法。 */
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
