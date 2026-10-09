/* §7.5 隐状态绑定的审计检查（roundtable ns-001）。
 *
 * 这三条不是风格提醒，是可机检的断言。它们的共同形状是：某个数字跟着控件
 * 状态漂移，而它旁边的文案没跟着漂 —— 本项目真实踩过的坑（预算告警的分子
 * 来自 rangeStats，跟顶栏范围走，告警条却一个字都不说自己是哪个子集）。
 *
 * 检查对象是渲染函数，不是源码文本：手写字符串渲染没有可静态分析的绑定关系，
 * 能验的只有「换掉状态之后输出变没变、变的部分有没有自报口径」。
 */
import * as fs from 'node:fs'
import { describe, it, expect } from 'vitest'
import {
  renderSettings, renderProjects, renderOverview, renderModels, renderSessions,
  budgetAlertHtml,
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

/* §7.7 定位措辞：这个账本覆盖全部检测到的工具，界面不许把 pi 当主语。
 * 这一批是扫**已发布适配器**的回归护栏 —— 文案写错时不会有任何编译错误，
 * 只会安静地把一个 6 工具的账本说成 pi 的附庸。 */
describe('§7.7 定位措辞', () => {
  it('全库文案不得出现把 pi 摆进主位的措辞', () => {
    const data = JSON.parse(fs.readFileSync('../.cache/dashboard.json', 'utf8'))
    const st = defaultUiState()
    st.budgetUsd = 200
    const cmp = compareSources(data, st)
    const pages = {
      overview: renderOverview(data, st, cmp, '7d'),
      models: renderModels(data, st, cmp, '7d'),
      sessions: renderSessions(data, st, cmp, '7d'),
      projects: renderProjects(data, st, cmp, '7d'),
      settings: renderSettings(data, st, cmp, '7d', '')
    }
    // 「外部 + $」把其余工具说成附属品；正确写法是「其他 N 个工具」。
    for (const [name, html] of Object.entries(pages)) {
      expect(html, name + ' 出现了「外部 $」').not.toContain('外部 $')
      // 自定义价格源已删除，声明它就是 §7.3 的撒谎。
      expect(html, name + ' 仍宣称有自定义价格源').not.toContain('自定义')
    }
    // 主语位置不许出现「pi 侧已匹配」这类把 pi 抬成主体的标签。
    expect(pages.models).not.toContain('pi 侧已匹配单价')
    expect(pages.models).toContain('已匹配单价')
  })

  /* 真的只有 pi 才有的数据，限定词必须留着 —— 删掉它就是撒谎，比措辞偏向
   * 更严重。所以这两个断言是双向的：既不许抬高 pi，也不许抹掉真实的边界。 */
  it('仅 pi 才有的区块必须保留限定词，并指明其余工具的用量在哪看', () => {
    const data = JSON.parse(fs.readFileSync('../.cache/dashboard.json', 'utf8'))
    const html = renderOverview(data, defaultUiState(), compareSources(data, defaultUiState()), '7d')
    expect(html).toContain('仅 pi 侧模型')
    expect(html).toContain('外部工具的用量见下方各工具行')
  })

/* 两个来源的分数不许求和：pi 与外部有同名模型，求和会重复计数（§7.1）。
   * 同时钉住量词：那个数字是**模型**行数，不是工具数 —— 写成「N 个工具」会
   * 把 17 个模型说成 17 个工具（本机只有 5 个外部工具）。 */
  it('单价覆盖 KPI 并列两个分数，不合并；量词是模型不是工具', () => {
    const data = JSON.parse(fs.readFileSync('../.cache/dashboard.json', 'utf8'))
    const html = renderModels(data, defaultUiState(), compareSources(data, defaultUiState()), '7d')
    const m = html.match(/已匹配单价[\s\S]{0,120}?(\d+)\/(\d+) · (\d+)\/(\d+)/)
    expect(m, 'KPI 未并列两个分数').not.toBeNull()
    // 主标签本身不再是「pi 侧…」
    expect(html).not.toMatch(/pi 侧已匹配单价/)
    expect(html).toContain('个模型')
    expect(html).not.toMatch(/外部 \d+ 个工具/)
  })
})

describe('总览页同一屏的两份总额', () => {
  it('日序列是全工具合计，热力图 caption 必须点名来源，不得共用「范围内合计」', () => {
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
        expect(c).toMatch(/全部工具|pi 侧/)
      }
    }
  })
})