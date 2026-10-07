/**
 * 套餐/限额进度：对标 CodeBurn「限额 · 已用」。
 * 配置存 localStorage；用量来自 external 工具的 days 聚合。
 */
import { externalSummary, type Policy } from './pricing'

export interface QuotaPlan {
  id: string
  label: string
  /** 用量匹配的工具 id（external.tools[].tool），或 'all' */
  tool: string
  /** 窗口：5h / 7d / 30d / month */
  window: '5h' | '7d' | '30d' | 'month'
  /** 限额：token 数（百万记也可，这里用绝对 token）或 0=未设 */
  limitTokens: number
  limitUsd: number
}

const KEY = 'tokanary-quotas-v1'

export function loadQuotas(): QuotaPlan[] {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return defaultQuotas()
    const o = JSON.parse(raw)
    if (Array.isArray(o) && o.length) return o
  } catch { /* ignore */ }
  return defaultQuotas()
}

export function saveQuotas(plans: QuotaPlan[]) {
  try { localStorage.setItem(KEY, JSON.stringify(plans)) } catch { /* ignore */ }
}

function defaultQuotas(): QuotaPlan[] {
  return [
    { id: 'codex', label: 'Codex', tool: 'codex', window: '7d', limitTokens: 0, limitUsd: 0 },
    { id: 'zcode', label: 'ZCode', tool: 'zcode', window: '7d', limitTokens: 0, limitUsd: 0 },
    { id: 'mimo', label: 'Xiaomi MiMo', tool: 'xiaomi-mimo', window: '30d', limitTokens: 0, limitUsd: 0 }
  ]
}

function windowDays(w: QuotaPlan['window']): number {
  if (w === '7d') return 7
  if (w === '30d') return 30
  return 30 // month ≈ 30d 近似
}

export interface QuotaUsage {
  plan: QuotaPlan
  tokens: number
  cost: number
  tokenPct: number | null
  costPct: number | null
  daysLeft: number | null
}

/**
 * 按套餐窗口汇总工具用量（token 绝对值 + 费用）。
 *
 * 费用口径复用 pricing.externalSummary：该工具的 unit = 全量费用 ÷ 全量 token × 1e6，
 * 是按它真实四段 token 构成加权出来的 $/1M（normalizeCost + costOfTokens 逐模型算完
 * 再汇总），与总览、跨工具总览同源。旧稿在这里退回「四个单价算术平均」，缓存占大头的
 * 工具会被系统性高估/低估；而 external.tools[] 从不带 unit 字段，那条 fallback 是
 * 唯一生效路径，等于全站限额费用都建在一个错的均价上。
 */
export function computeQuotas(data: any, plans: QuotaPlan[], policy: Policy = 'ratio10'): QuotaUsage[] {
  const tools = data?.external?.tools || []
  const ext = externalSummary(data, policy)
  const unitByTool: Record<string, number> = {}
  ;(ext ? ext.rows : []).forEach(function (r: any) {
    const id = (r.t && (r.t.tool || r.t.label)) || r.label
    if (id) unitByTool[id] = r.unit || 0
  })
  const out: QuotaUsage[] = []
  for (const plan of plans) {
    const selected = plan.tool === 'all'
      ? tools
      : tools.filter((t: any) => t.tool === plan.tool || t.label === plan.tool)
    let tokens = 0
    let cost = 0
    const days = windowDays(plan.window)
    const now = Date.now()
    for (const t of selected) {
      const unit = unitByTool[t.tool || t.label] || 0
      for (const d of t.days || []) {
        if (!d?.d) continue
        const t0 = new Date(d.d + 'T00:00:00').getTime()
        if (!isFinite(t0)) continue
        if (now - t0 > days * 86400000) continue
        const tok = Number(d.total || 0) ||
          Number(d.input || 0) + Number(d.output || 0) + Number(d.cacheRead || 0) + Number(d.cacheWrite || 0)
        tokens += tok
        cost += unit * tok / 1e6
      }
    }
    out.push({
      plan,
      tokens,
      cost,
      tokenPct: plan.limitTokens > 0 ? Math.min(100, tokens / plan.limitTokens * 100) : null,
      costPct: plan.limitUsd > 0 ? Math.min(100, cost / plan.limitUsd * 100) : null,
      daysLeft: plan.window === '7d' ? 7 : plan.window === '5h' ? 1 : 30
    })
  }
  return out
}

export function quotaPct(u: QuotaUsage): number | null {
  return u.tokenPct ?? u.costPct
}