/**
 * 窗口用量：按滚动窗口汇总外部 CLI 的实测用量。
 *
 * 这里报的是「用了多少」，不是「额度还剩多少」。各家额度存在服务端，本机读不到。
 * 旧稿让用户自己填一个上限，再拿实测 token 去除 —— 分母是随手编的数字，得出的
 * 百分比看着精确、实际不能支撑任何决策。旧稿另有三处硬伤，一并作废：
 *   - 窗口类型允许 '5h'，而 windowDays 的兜底 `return 30` 把它当 30 天统计用量；
 *   - daysLeft 返回的是窗口长度而不是剩余时间，且全项目没有一处读它；
 *   - 默认三条套餐全是「额度上限未设置」，是三行纯噪音。
 *
 * 窗口只有近 7 天 / 近 30 天两个：外部 CLI 的用量日志是逐日粒度（pi 侧才有 hours），
 * 算不出 5 小时窗口，不提供测不出来的窗口。
 */
import { externalSummary, filterDaysByRange, rangeAnchor, sumDayFields, type Policy } from './pricing'

export type UsageWindowKey = '7d' | '30d'

export const USAGE_WINDOWS: Array<{ key: UsageWindowKey; label: string }> = [
  { key: '7d', label: '近 7 天' },
  { key: '30d', label: '近 30 天' }
]

export interface WindowUsage {
  /** 工具 id（external.tools[].tool），缺省时回退到 label */
  tool: string
  label: string
  /** key = 窗口键（USAGE_WINDOWS 的 key） */
  byWindow: Record<string, { tokens: number; cost: number }>
  /** 近 30 天 token，排序用 */
  tokens: number
}

/**
 * 按窗口汇总外部工具用量，近 30 天无动静的工具不返回。按近 30 天 token 降序。
 *
 * 费用口径复用 pricing.externalSummary：该工具的 unit = 全量费用 ÷ 全量 token × 1e6，
 * 是按它真实四段 token 构成加权出来的 $/1M，与总览、跨工具总览同源。窗口边界走
 * filterDaysByRange(anchor = rangeAnchor)，与顶栏范围切换共用同一套日算法，
 * 不在这里另写一份 —— 旧稿的 windowDays 就是另写一份，然后和实际窗口对不上。
 */
export function windowUsage(data: any, policy: Policy = 'ratio10'): WindowUsage[] {
  const tools = data?.external?.tools || []
  if (!tools.length) return []
  const anchor = rangeAnchor(data)
  if (!anchor) return []
  const ext = externalSummary(data, policy)
  const unitByTool: Record<string, number> = {}
  ;(ext ? ext.rows : []).forEach(function (r: any) {
    const id = (r.t && (r.t.tool || r.t.label)) || r.label
    if (id) unitByTool[id] = r.unit || 0
  })
  const out: WindowUsage[] = []
  for (const t of tools) {
    const id = t.tool || t.label || ''
    const unit = unitByTool[id] || 0
    const byWindow: Record<string, { tokens: number; cost: number }> = {}
    for (const w of USAGE_WINDOWS) {
      const fields = sumDayFields(filterDaysByRange(t.days || [], w.key, anchor))
      const tokens = fields.total
      byWindow[w.key] = { tokens: tokens, cost: unit * tokens / 1e6 }
    }
    const tokens = byWindow['30d'].tokens
    if (!(tokens > 0)) continue
    out.push({ tool: id, label: t.label || id, byWindow: byWindow, tokens: tokens })
  }
  out.sort(function (a, b) { return b.tokens - a.tokens })
  return out
}