/* §7.5 隐状态绑定的审计检查（roundtable ns-001）。
 *
 * 这三条不是风格提醒，是可机检的断言。它们的共同形状是：某个数字跟着控件
 * 状态漂移，而它旁边的文案没跟着漂 —— 本项目真实踩过的坑（预算告警的分子
 * 来自 rangeStats，跟顶栏范围走，告警条却一个字都不说自己是哪个子集）。
 *
 * 检查对象是渲染函数，不是源码文本：手写字符串渲染没有可静态分析的绑定关系，
 * 能验的只有「换掉状态之后输出变没变、变的部分有没有自报口径」。
 */
import { describe, it, expect } from 'vitest'
import {
  renderSettings, renderProjects, renderOverview, budgetAlertHtml,
  BUDGET_SCOPE_LABEL, compareSources, defaultUiState, monthToDateCost
} from './render'

/** 一天 1000 token 的合成数据：跨月铺开，好让「本月至今」不等于「全部」。 */
function scopedFixture() {
  const days: any[] = []
  const push = (d: string, total: number) => {
    days.push({
      d, cacheRead: total * 0.5, cacheWrite: total * 0.1, input: total * 0.3,
      output: total * 0.1, total
    })
  }
  // 上月与本月都铺满，两月的量刻意不同：这样「本月至今」与「全部」必然不等，
  // 断言才有能力把「用了范围聚合的数」和「用了本月至今的数」区分开。
  for (let i = 1; i <= 20; i++) push('2026-08-' + String(i).padStart(2, '0'), 1000)
  for (let i = 1; i <= 10; i++) push('2026-09-' + String(i).padStart(2, '0'), 1000)
  return {
    days,
    dayModel: days.map((d) => ({ d, key: 'm1', total: d.total })),
    models: [{ key: 'm1', name: 'M1', unit: 0.001, total: 30000 }],
    sessions: [],
    sessionsAll: [],
    projects: []
  }
}

/** 从渲染结果里抽出告警条上的「已用」金额字符串。 */
function usedAmount(html: string): string | null {
  const m = html.match(new RegExp(BUDGET_SCOPE_LABEL + '\\s*已用 <b>\\$([\\d.,kK]+)</b>'))
  return m ? m[1] : null
}

describe('§7.5 隐状态绑定 · 审计三问', () => {
  /* 问 1：关掉 / 切换筛选器后数值变了吗，而文案没跟着变？ */
  it('问1 预算的「已用」在四档范围下不变 —— 分母是月预算，分子就得是月', () => {
    const data = scopedFixture()
    const st = defaultUiState()
    st.budgetUsd = 100
    const seen = (['today', '7d', '30d', 'all'] as const).map((r) => {
      const html = renderSettings(data, st, compareSources(data, st), r, '')
      return usedAmount(html)
    })
    expect(seen[0]).not.toBeNull()
    for (const s of seen) expect(s).toBe(seen[0])
  })

  it('问1 monthToDateCost 只取日历月，与范围无关', () => {
    const byDay = { '2026-08-01': 5, '2026-08-02': 7, '2026-09-01': 3, '2026-09-02': 4 }
    // 「当月」取真实日历月，不是数据最新日所在的月：月预算本来就针对日历月，
    // 用数据最新日反推会在跨越月末时凭空多出或少掉一个月。
    expect(monthToDateCost(byDay, new Date('2026-09-02T00:00:00'))).toBeCloseTo(7, 9)
    expect(monthToDateCost(byDay, new Date('2026-08-02T00:00:00'))).toBeCloseTo(12, 9)
    // 默认取系统当天所在月。
    expect(monthToDateCost(byDay)).toBeCloseTo(monthToDateCost(byDay, new Date()), 9)
    expect(monthToDateCost({}, new Date('2026-09-02T00:00:00'))).toBe(0)
    expect(monthToDateCost({ x: 1 }, new Date('2026-09-02T00:00:00'))).toBe(0)
  })

  /* 问 2：同一屏里同一个词另有数值，且那个数没标范围？ */
  it('问2 项目页的预算条也写出自述区间', () => {
    const data = scopedFixture()
    const st = defaultUiState()
    st.budgetUsd = 100
    const html = renderProjects(data, st, compareSources(data, st), '7d')
    const ba = html.match(/<div class="note budget-[^"]*">[\s\S]*?<\/div>/)
    expect(ba).not.toBeNull()
    expect(ba![0]).toContain(BUDGET_SCOPE_LABEL)
  })

  it('问2 「已用」与「合计」这类聚合词必须跟在范围名后面', () => {
    const html = budgetAlertHtml(60, 100, BUDGET_SCOPE_LABEL)
    // 聚合词的前 20 字内必须有范围标识，否则读者无从判断分母是谁。
    const i = html.indexOf('已用')
    expect(i).toBeGreaterThan(-1)
    expect(html.slice(Math.max(0, i - 24), i)).toContain(BUDGET_SCOPE_LABEL)
  })

  /* 问 3：告警等级或阈值随隐藏筛选器升降？ */
  it('问3 告警等级不随范围筛选器变化', () => {
    const data = scopedFixture()
    const st = defaultUiState()
    st.budgetUsd = 100
    const levels = (['today', '7d', '30d', 'all'] as const).map((r) => {
      const html = renderProjects(data, st, compareSources(data, st), r)
      const m = html.match(/class="note budget-(ok|warn|err)"/)
      return m ? m[1] : null
    })
    expect(levels[0]).not.toBeNull()
    for (const l of levels) expect(l).toBe(levels[0])
  })

  it('告警条写不出区间就不渲染 —— 宁可不渲染，不渲染读不出分母的告警', () => {
    expect(budgetAlertHtml(60, 100, '')).toBe('')
    expect(budgetAlertHtml(60, 100, BUDGET_SCOPE_LABEL)).toContain('budget-')
    // 分母为 0 一律关闭，0 不是「还没开始花钱」，是「没设预算」。
    expect(budgetAlertHtml(60, 0, BUDGET_SCOPE_LABEL)).toBe('')
  })

  /* 回归：修复前 budgetAlertHtml 的签名里没有范围参数，输出恒定不含任何限定词。
   * 这个断言把「签名不再可能漏掉口径」这件事钉在类型上。 */
  it('budgetAlertHtml 强制要求口径标签（签名层防回归）', () => {
    // @ts-expect-error 少传 scopeLabel 应当编译失败
    budgetAlertHtml(60, 100)
  })
})

describe('总览页同一屏的两份总额', () => {
  it('日序列是全工具合计，热力图 caption 必须点名 pi 侧，不得共用「范围内合计」', () => {
    const st = defaultUiState()
    const data: any = {
      days: [{ d: '2026-09-10', cacheRead: 1, cacheWrite: 0, input: 1, output: 0, total: 2 }],
      dayModel: [{ d: '2026-09-10', key: 'm1', total: 2 }],
      models: [{ key: 'm1', name: 'M1', unit: 0.001, total: 2 }],
      sessions: [], sessionsAll: [], projects: [],
      hours: [{ h: '2026-09-09T14:00:00', total: 5 }],
      external: { tools: [{ id: 'e1', label: 'Ext', days: [{ d: '2026-09-10', total: 99 }] }] }
    }
    const html = renderOverview(data, st, {}, '7d')
    const caps = html.match(/<div class="hm-cap[^"]*">([^<]*)<\/div>/g) || []
    for (const c of caps) {
      // 出现「合计」就必须同时出现来源限定；这是 §7.5 问 2 的可执行形式。
      if (c.includes('合计')) {
        expect(c).toMatch(/pi 侧|全工具/)
      }
    }
  })
})