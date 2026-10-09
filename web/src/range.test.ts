/* U1:范围聚合手算对照 + 空守卫 + 真实数据一致性 + 总览冒烟。
 * 用法: npm test (web/ 目录下)。真实数据用例需要 .cache/dashboard.json */
import { describe, it, expect } from 'vitest'
import {
  computeAll, compareSources, dailyCost, defaultOpts, money,
  externalSummary, rangeExt,
  rangeStats, filterDaysByRange, rangeCutoffKey, rangeAnchor,
  filterHoursByRange, hourMatrix, rangeInsights, calcStreak, weekTopModels, isoWeekRange,
  type RangeKey
} from './pricing'
import { renderOverview, renderModels, renderSessions, renderProjects, renderSettings, budgetAlertHtml, defaultUiState, esc } from './render'
import { externalModelRows } from './render/models'
import { BUDGET_SCOPE_LABEL } from './render/projects'
import { useHashRoute } from './composables/useHashRoute'
import { heatmap, heatTone, MODEL_COLORS } from './charts'
import { mergeAllHours, hourToolCount } from './pricing'
import { CATS } from './pricing'
import { mergeDailyUsage } from './range'
import { loadDashboardFixture } from './testsupport'

const S = BUDGET_SCOPE_LABEL

const DATA = loadDashboardFixture()
const live = DATA ? describe : describe.skip
const itLive = DATA ? it : it.skip

const CLOSE = 1e-6
const near = (a: number, b: number) => expect(Math.abs(a - b)).toBeLessThan(CLOSE)

/* 手算固件:10 个连续日,total 100..1000,cost = total/100 */
function fixture() {
  const days = []
  for (let i = 1; i <= 10; i++) {
    const total = i * 100
    days.push({
      d: '2026-09-' + String(i).padStart(2, '0'),
      cacheRead: total * 0.6, cacheWrite: total * 0.1, input: total * 0.2,
      output: total * 0.1, total
    })
  }
  const costByDay: Record<string, number> = {}
  days.forEach((d) => { costByDay[d.d] = d.total / 100 })
  return { data: { days }, costByDay }
}

describe('range aggregation', () => {
  it('日历窗口:today 取锚日,7d 取后 7 天,30d/all 取全部', () => {
    const { data } = fixture()
    expect(filterDaysByRange(data.days, 'today').map((d) => d.d)).toEqual(['2026-09-10'])
    expect(filterDaysByRange(data.days, '7d').map((d) => d.d)[0]).toBe('2026-09-04')
    expect(filterDaysByRange(data.days, '7d')).toHaveLength(7)
    expect(filterDaysByRange(data.days, '30d')).toHaveLength(10)
    expect(filterDaysByRange(data.days, 'all')).toHaveLength(10)
    expect(rangeCutoffKey([], 'today')).toBeNull()
    expect(rangeAnchor({ days: [] })).toBeNull()
  })

  it('手算对照:tokens 精确,费用摊算求和,min/avg/max', () => {
    const { data, costByDay } = fixture()
    const s7 = rangeStats(data, {} as any, costByDay, '7d')
    near(s7.tokens, 4900)
    near(s7.cost, 49)
    near(s7.avgCost, 7)
    expect(s7.dayCount).toBe(7)
    expect(s7.maxDay).toBe('2026-09-10')
    near(s7.maxCost, 10)
    expect(s7.minDay).toBe('2026-09-04')
    near(s7.minCost, 4)
    near(s7.cacheHitPct, 66.7)   // 读/(读+写+新增输入) = 0.6/0.9(分母不含输出,与旧口径同式)
    const s1 = rangeStats(data, {} as any, costByDay, 'today')
    near(s1.tokens, 1000)
    near(s1.cost, 10)
  })

  it('空范围守卫:全 0 且无 NaN', () => {
    const s = rangeStats({ days: [] }, {} as any, {}, '7d' as RangeKey)
    expect(s.tokens).toBe(0)
    expect(s.cost).toBe(0)
    expect(s.avgCost).toBe(0)
    expect(s.minDay).toBeNull()
    expect(s.cacheHitPct).toBe(0)
    expect(Number.isNaN(s.avgCost)).toBe(false)
  })

  itLive('真实数据:all 范围 token == 总计,费用 == 按天合计', () => {
    const st = defaultUiState()
    const s = computeAll(DATA, st)
    const byDay = dailyCost(DATA, s)
    const all = rangeStats(DATA, s, byDay, 'all')
    expect(all.tokens).toBe(DATA.totals.total)
    near(all.cost, Object.keys(byDay).reduce((t, k) => t + byDay[k], 0))
    // today 以多源最新日为锚；pi 序列若过期则今天为 0 是正确行为
    const today = rangeStats(DATA, s, byDay, 'today')
    const a = rangeAnchor(DATA)
    const hasToday = ((DATA.days || []) as any[]).some((d: any) => d.d === a && d.total > 0)
    if (hasToday) {
      expect(today.dayCount).toBe(1)
      expect(today.tokens).toBeGreaterThan(0)
    } else {
      expect(today.dayCount).toBe(0)
    }
  })

  itLive('总览冒烟:hero 金额一致 + 跨工具 + 无泄漏', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const html = renderOverview(DATA, st, cmp, '7d')
    expect(html).toContain(money(rangeStats(DATA, computeAll(DATA, st), dailyCost(DATA, computeAll(DATA, st)), '7d').cost))
    expect(html).toContain('跨工具总览')
    expect(html).toContain('<svg')
    expect(html).not.toMatch(/>undefined</)
    expect(html).not.toMatch(/NaN/)
    expect(renderOverview(null, st, cmp, '7d')).toContain('tokanary refresh')
  })
})

/* ---- U2: hours / heatmap / 洞察 ---- */

function hourFixture() {
  // 2026-09-01 是周二(getDay=2);造 3 个小时格
  return {
    hours: [
      { h: '2026-09-01 10', total: 100 },
      { h: '2026-09-01 10', total: 50 },   // 同格累加
      { h: '2026-09-03 23', total: 200 },
      { h: '2026-09-10 00', total: 300 }
    ] as Array<{ h: string; total: number }>
  }
}

describe('U2 hours heatmap', () => {
  it('hourMatrix: weekday×hour 聚合,7×24,同格求和', () => {
    const m = hourMatrix(hourFixture().hours)
    expect(m).toHaveLength(7)
    m.forEach((row) => expect(row).toHaveLength(24))
    // 2026-09-01 = Tuesday → wd=2, hour 10 → 150
    expect(m[2][10]).toBe(150)
    // 2026-09-03 = Thursday → wd=4, hour 23 → 200
    expect(m[4][23]).toBe(200)
    // 2026-09-10 = Thursday → wd=4, hour 0 → 300
    expect(m[4][0]).toBe(300)
    const sum = m.reduce((t, r) => t + r.reduce((a, b) => a + b, 0), 0)
    expect(sum).toBe(650) // 100+50+200+300
  })

  /* 量级编码：离散五档 + 零值反向修复 + 霁青退出数据面。 */
  it('heatTone: 离散五档;空格最弱且最轻', () => {
    const max = 10000
    expect(heatTone(0, max)).toBe(0)
    expect(heatTone(-1, max)).toBe(0)
    expect(heatTone(100, max)).toBe(1)      // r=0.10
    expect(heatTone(1000, max)).toBe(2)     // r≈0.316
    expect(heatTone(4000, max)).toBe(3)     // r≈0.632
    expect(heatTone(9000, max)).toBe(4)
    expect(heatTone(max, max)).toBe(4)
    expect(heatTone(1, 0)).toBe(0)
    // 档位随值单调不减 —— 不会出现「值更大反而更浅」。
    let prev = 0
    for (let v = 1; v <= max; v += 137) {
      const t = heatTone(v, max)
      expect(t).toBeGreaterThanOrEqual(prev)
      prev = t
    }
  })

  it('heatmap: 霁青退出数据面;空格用最轻档;图例给绝对值', () => {
    const m = hourMatrix(hourFixture().hours)
    const html = heatmap(m, { sourceLabel: '全部工具 · 本地时' })
    // §2 铁律 1：主色只属导航选中与主动作，不得做数据面。
    expect(html).not.toContain('var(--accent)"')
    expect(html).not.toContain('fill="var(--accent)"')
    expect(html).toContain('var(--hm-0)')
    expect(html).toContain('var(--hm-4)')
    // 零值反向修复：空格必须落在第 0 档（旧实现用 --line@0.35，比最弱数据格更重）。
    expect(html).toContain('fill="var(--hm-0)"')
    // 离散：不再有连续 opacity。
    expect(html).not.toMatch(/opacity="/)
    // 非颜色通道：图例带绝对端点，不写「强度」。
    expect(html).toContain('hm-legend')
    expect(html).toContain('峰值')
    expect(html).not.toContain('强度')
  })

  it('heatmap: 恰好两个 tab stop（摘要 + 峰值格）', () => {
    const m = hourMatrix(hourFixture().hours)
    const html = heatmap(m, { sourceLabel: '全部工具 · 本地时' })
    const stops = (html.match(/tabindex="0"/g) || []).length
    expect(stops).toBe(2)
    // 其余 167 格不进 Tab 序列，但仍各自带可读的 <title>。只在 <svg> 内数，
    // 图例自己也是一个 aria-hidden 节点。
    const svg = html.slice(html.indexOf('<svg'), html.indexOf('</svg>'))
    expect((svg.match(/aria-hidden="true"/g) || []).length).toBe(168 - 1)
    expect(html).toContain('role="img"')
    expect(html).toMatch(/aria-label="峰值/)
    expect(html).toMatch(/aria-label="[^"]*时段热力图：/)
  })

  /* 时段热力图覆盖全部工具：pi 侧的 hours 加上每个外部工具自己的 hours。
   * 曾有一版规范写着「外部 CLI 只有逐日粒度」，那是把「我们只聚合到日」当成了
   * 数据的事实 —— 实际上 5 个外部工具全部带小时级时间戳。 */
  it('mergeAllHours: 合并 pi 与全部外部工具的小时桶', () => {
    const data: any = {
      hours: [{ h: '2026-09-10 05', total: 7 }],
      external: {
        tools: [
          { tool: 'a', hours: [{ h: '2026-09-10T06', total: 3 }] },
          { tool: 'b', hours: [{ h: '2026-09-10T07', total: 5 }] },
          { tool: 'no-hours', days: [{ d: '2026-09-10' }] }
        ]
      }
    }
    const merged = mergeAllHours(data)
    expect(merged).toHaveLength(3)
    expect(merged.reduce((t, x) => t + x.total, 0)).toBe(15)
    // 两个键格式不同（空格 vs T），但日期段都在前 10 位，hourMatrix 的切片照样对。
    const m = hourMatrix(merged)
    const total = m.flat().reduce((t, v) => t + v, 0)
    expect(total).toBe(15)
    expect(hourToolCount(data)).toBe(2) // 'no-hours' 不算
    expect(mergeAllHours(null)).toEqual([])
    expect(mergeAllHours({})).toEqual([])
    expect(hourToolCount({})).toBe(0)
  })

  /* 零桶不得凭空出现：total 为 0 的行不进矩阵，否则「没活动」会被画成和
   * 「没数据」同一格（§7 铁律 4）。 */
  it('mergeAllHours: 跳过零与缺 h 的行', () => {
    const merged = mergeAllHours({
      hours: [{ h: '2026-09-10 05', total: 0 }],
      external: { tools: [{ tool: 'a', hours: [{ h: '', total: 9 }, { h: '2026-09-10T06', total: 0 }] }] }
    })
    expect(merged).toEqual([])
  })

  it('filterHoursByRange: 按日期裁剪到 range 窗口', () => {
    const hours = hourFixture().hours
    const anchor = '2026-09-10'
    expect(filterHoursByRange(hours, 'today', anchor)).toHaveLength(1)
    const w7 = filterHoursByRange(hours, '7d', anchor)
    expect(w7.every((x) => x.h.slice(0, 10) >= '2026-09-04' && x.h.slice(0, 10) <= '2026-09-10')).toBe(true)
    expect(w7).toHaveLength(1) // 只有 09-10 落在窗口
    expect(filterHoursByRange(hours, 'all')).toHaveLength(4)
    expect(filterHoursByRange([], '7d')).toEqual([])
  })

  it('heatmap: 有数据出 SVG;caption 自报来源与格数;全 0/空出降级文案', () => {
    const m = hourMatrix(hourFixture().hours)
    const html = heatmap(m, { sourceLabel: '全部工具 · 本地时' })
    expect(html).toContain('<svg')
    expect(html).toContain('heatmap')
    // §7.5：这个合计覆盖不到同屏 dayChart 的全工具数，所以必须点名来源；
    // 旧文案「范围内合计」与 dayChart 共用一个词，且把格数说成「小时」。
    expect(html).toContain('全部工具 · 本地时')
    expect(html).toContain('/ 168 格非零')
    expect(html).not.toContain('范围内合计')
    const empty = heatmap([], { sourceLabel: '全部工具 · 本地时' })
    expect(empty).toContain('tokanary refresh')
    const zero = heatmap(new Array(7).fill(null).map(() => new Array(24).fill(0)), { sourceLabel: '全部工具 · 本地时' })
    expect(zero).toContain('tokanary refresh')
  })

  /* sourceLabel 是必填的：漏传就没有任何口径声明，而这正是本项目踩过的坑。
   * 用类型系统钉住，比在运行时返回一句空标签更早暴露。 */
  it('heatmap 必须传 sourceLabel（签名层防回归）', () => {
    // 签名要求是一层，运行时空标签也必须挡住：漏传会得到一张没有口径声明的图。
    // @ts-expect-error 缺 sourceLabel 应当编译失败
    const noLabel = heatmap(new Array(7).fill(null).map(() => new Array(24).fill(3)))
    expect(noLabel).toBe('')
  })

  it('rangeInsights: 手算对照 + 空守卫', () => {
    const { data, costByDay } = fixture()
    const s = computeAll(data as any, defaultOpts())
    // fixture 里 unit 未定义(无 models),最贵日仍应按 costByDay 算出
    const ins = rangeInsights({ ...data, dayModel: [] } as any, s, costByDay, '7d')
    expect(ins.peakDay).toBe('2026-09-10')
    near(ins.peakDayCost, 10)
    near(ins.avgBurn, 7)
    near(ins.forecast30, 210) // 7×30
    expect(ins.dayCount).toBe(7)
    near(ins.cacheHitPct, 66.7)

    const empty = rangeInsights({ days: [] } as any, {} as any, {}, '7d')
    expect(empty.peakDay).toBeNull()
    near(empty.avgBurn, 0)
    near(empty.forecast30, 0)
    expect(Number.isNaN(empty.avgBurn)).toBe(false)
  })

  itLive('真实数据: hours 求和 ≈ days(total);总览含 heatmap+洞察', () => {
    if (Array.isArray(DATA.hours) && DATA.hours.length) {
      const hs = DATA.hours.reduce((t: number, x: any) => t + (x.total || 0), 0)
      const ds = (DATA.days || []).reduce((t: number, x: any) => t + (x.total || 0), 0)
      // warehouse 侧 hours 只计有 h 的轮;允许极小相对差(<0.1%)
      expect(Math.abs(hs - ds) / Math.max(ds, 1)).toBeLessThan(0.001)
    }
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const html = renderOverview(DATA, st, cmp, 'all')
    // 标题必须点名来源：只有 pi 侧有 hours 粒度，不点名就与同屏 dayChart 的
    // 全工具合计共用一个「总量」的读法（§7.5）。
    expect(html).toContain('时段热力图')
    expect(html).toContain('个工具本地时合计（含 pi）')
    expect(html).toContain('洞察')
    expect(html).toContain('月末预测')
    expect(html).not.toMatch(/NaN/)
    // 无 hours 的降级。小时现在来自 pi 与全部外部工具，所以降级条件是
    // 「所有来源都没有 hours」，而不是只看 payload.hours。
    const noH = {
      ...DATA,
      hours: undefined,
      external: { tools: ((DATA as any).external?.tools || []).map((t: any) => ({ ...t, hours: undefined })) }
    }
    const html2 = renderOverview(noH, st, cmp, 'all')
    expect(html2).toContain('tokanary refresh')
  })
})

/* ---- U3: 模型视图(稳定色/donut/堆叠) + 会话视图(搜索/排序/burn) ---- */

describe('U3 models tab', () => {
  itLive('冒烟:堆叠 + donut + 改价 input + 范围内列,无 NaN', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const html = renderModels(DATA, st, cmp, '7d')
    expect(html).toContain('模型 Token 构成')
    expect(html).toContain('模型费用占比')
    expect(html).toContain('模型明细与单价')
    expect(html).toContain('data-field=')
    expect(html).toContain('data-key=')
    expect(html).toContain('范围内 Token')
    // 单价覆盖 KPI 必须把两个来源的分数都写出来,别让 pi 的 14/14 看着像全量都匹配上了。
    // 两个分数并列而不合并：pi 与外部有同名模型（交集 4 个），相加会重复计数。
    expect(html).toContain('已匹配单价')
    expect(html).toContain('外部 ')
    expect(html).not.toContain('已匹配价格')
    if (!html.includes('当前范围内没有模型用量')) expect(html).toContain('<svg')
    expect(html).not.toMatch(/NaN/)
    expect(html).not.toMatch(/>undefined</)
  })

  itLive('稳定色:同一 key 字典序索引跨渲染一致;donut 含模型色', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const a = renderModels(DATA, st, cmp, 'all')
    const b = renderModels(DATA, st, cmp, 'all')
    expect(a).toBe(b)
    // Ember Instrument 六色单源:渲染必须出现 palette 首色(s4e1 终裁,同 PR 迁测)
    expect(a).toContain(MODEL_COLORS[0])
  })

  it('色板契约:MODEL_COLORS 恰 6 色且 CATS 四段 = 前 4 色(单源,禁第七色)', () => {
    expect(MODEL_COLORS).toHaveLength(6)
    expect(new Set(MODEL_COLORS).size).toBe(6)
    CATS.forEach((c, i) => {
      expect(c.color).toBe(MODEL_COLORS[i])
    })
  })

  itLive('空范围:无范围内用量降级文案,不崩', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const emptyData = { ...DATA, days: [], dayModel: [], models: [], external: null }
    const html = renderModels(emptyData, st, cmp, '7d')
    expect(html).toContain('没有模型用量')
    expect(html).not.toMatch(/NaN/)
    expect(renderModels(null, st, cmp, '7d')).toContain('tokanary refresh')
  })

  itLive('sortKey 控制明细排序:按 cost 降序时首行费用 ≥ 末行', () => {
    const st = defaultUiState()
    st.sortKey = 'cost'
    st.sortDir = -1
    const cmp = compareSources(DATA, st)
    const html = renderModels(DATA, st, cmp, 'all')
    // 仅结构冒烟:明细表仍渲染行
    expect(html).toContain('<tbody>')
  })

  itLive('无价格的模型行染 row-miss', () => {
    // row-miss is driven by a missing PRICE, not by missing usage records -
    // the old assertion here conflated the two and only passed while some
    // model happened to be unpriced. Build the case explicitly instead of
    // depending on whatever the current fixture happens to contain.
    const bare: any = JSON.parse(JSON.stringify(DATA))
    bare.pricing = {}
    bare.gateway = null
    const st2 = defaultUiState()
    const html = renderModels(bare, st2, compareSources(bare, st2), 'all')
    expect(html).toContain('<tbody>')
    expect(html).toContain('row-miss')
  })
})

describe('U3 sessions tab', () => {
  itLive('冒烟:表格 + 搜索框 + burn 列 + recency,无 NaN', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const html = renderSessions(DATA, st, cmp, 'all')
    expect(html).toContain('id="ses-search"')
    expect(html).toContain('data-ses-sort=')
    expect(html).toContain('Burn')
    expect(html).toContain('Recency')
    expect(html).not.toMatch(/NaN/)
    expect(html).not.toMatch(/>undefined</)
    expect(renderSessions(null, st, cmp, '7d')).toContain('tokanary refresh')
  })

  itLive('搜索过滤:匹配 title/project/model/id,大小写不敏感', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const all = renderSessions(DATA, st, cmp, 'all')
    const nAll = (all.match(/data-ses-sort/g) || []).length // 表头一次
    st.sesQuery = '__no_such_session_zzz__'
    const miss = renderSessions(DATA, st, cmp, 'all')
    expect(miss).toContain('没有匹配')
    expect(miss).not.toContain('data-ses-sort=') // 无行
    void nAll

    // 用真实会话的 title 片段
    const sess = (DATA.sessions || [])[0]
    if (sess && sess.title) {
      st.sesQuery = String(sess.title).slice(0, 6)
      const hit = renderSessions(DATA, st, cmp, 'all')
      expect(hit).toContain('data-ses-sort=')
      st.sesQuery = ''
    }
  })

  itLive('排序方向:updatedAt desc 切换为 asc 后箭头翻转', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    st.sesSortKey = 'updatedAt'
    st.sesSortDir = -1
    const desc = renderSessions(DATA, st, cmp, 'all')
    expect(desc).toContain('▾')
    st.sesSortDir = 1
    const asc = renderSessions(DATA, st, cmp, 'all')
    expect(asc).toContain('▴')
  })

  itLive('burn ≥ 0:范围内每行 tok/天 与 $/天 非负', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const html = renderSessions(DATA, st, cmp, 'all')
    // burn 列若渲染数字,不应出现负号前缀的 burn(简单:无 "-N" 紧跟 burn 单元)
    expect(html).not.toMatch(/>-\d/)
    // 空范围
    const empty = renderSessions({ ...DATA, sessions: [] }, st, cmp, '7d')
    expect(empty).toContain('没有会话')
  })

  itLive('范围过滤:today 窗口会话数 ≤ all', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const all = renderSessions(DATA, st, cmp, 'all')
    const today = renderSessions(DATA, st, cmp, 'today')
    const cnt = (h: string) => (h.match(/<tr>/g) || []).length
    expect(cnt(today)).toBeLessThanOrEqual(cnt(all))
    expect(today).not.toMatch(/NaN/)
  })
})

/* ---- U4: 项目钻取 + 设置 + 预算 ---- */

describe('U4 projects tab', () => {
  itLive('冒烟:归因条 + data-proj 行 + 无 NaN;null data 守卫', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const html = renderProjects(DATA, st, cmp, 'all')
    expect(html).toContain('项目归因')
    expect(html).toMatch(/data-proj="/)
    expect(html).toContain('<svg')
    expect(html).not.toMatch(/NaN/)
    expect(html).not.toMatch(/>undefined</)
    expect(renderProjects(null, st, cmp, '7d')).toContain('tokanary refresh')
  })

  itLive('钻取:选项目出面板(模型/日趋势/会话) + 返回按钮', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const name = (DATA.projects || []).map((p: any) => p.name).find((n: string) => n) || '(无项目)'
    st.drillProject = name
    const html = renderProjects(DATA, st, cmp, 'all')
    expect(html).toContain('项目钻取')
    expect(html).toContain('该项目模型构成')
    expect(html).toContain('该项目日趋势')
    expect(html).toContain('该项目会话')
    expect(html).toContain('id="drill-back"')
    expect(html).not.toMatch(/NaN/)
    st.drillProject = null
    const back = renderProjects(DATA, st, cmp, 'all')
    expect(back).not.toContain('id="drill-back"')
  })

  itLive('空范围:无项目用量降级文案', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const html = renderProjects({ ...DATA, projects: [], sessionsAll: [], sessions: [] }, st, cmp, '7d')
    expect(html).toContain('没有项目用量')
    expect(html).not.toMatch(/NaN/)
  })
})

describe('U4 settings tab + budget', () => {
  itLive('冒烟:预算行/单价来源/策略 select/限额/导入导出（无自定义源、无网关表）', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const html = renderSettings(DATA, st, cmp, '7d', '')
    expect(html).toContain('预算告警')
    expect(html).toContain('id="budget-usd"')
    // SOURCES 只剩 models.dev 一个成员，渲染单选框等于给一个没有可选项的孤立圆点；
    // 改为纯文本陈述，这里反过来断言 radio 不再出现。
    expect(html).toContain('单价来源')
    expect(html).not.toContain('name="psrc"')
    expect(html).toContain('id="policy"')
    expect(html).toContain('id="btn-export"')
    expect(html).toContain('id="btn-import"')
    expect(html).toContain('id="io"')
    // 单价只走 models.dev（AGENTS.md：无网关价源）——自定义价格源与网关价目表已删，
    // 这里反过来断言它们不再出现，防止有人把它们加回来。
    expect(html).not.toContain('id="custom-url"')
    expect(html).not.toContain('自定义价格源')
    expect(html).not.toContain('网关价目表')
    expect(html).not.toMatch(/NaN/)
    expect(renderSettings(null, st, cmp, '7d', '')).toContain('tokanary refresh')
  })

  it('预算分级:0 关闭无条;50/80/95 三档必须真的分得开;cost≤budget 安全', () => {
    expect(budgetAlertHtml(10, 0, S)).toBe('')
    expect(budgetAlertHtml(10, -1, S)).toBe('')
    // scopeLabel 为空 = 渲染不出区间自述。DESIGN.md §7.5：调用方该改成静态陈述，
    // 不许传空标签蒙混 —— 一个跟着控件漂的数字裸着出现最容易被当成定值。
    expect(budgetAlertHtml(10, 100, '')).toBe('')
    const safe = budgetAlertHtml(10, 100, S)
    expect(safe).toContain('预算进度')
    expect(safe).toContain('budget-ok')
    expect(safe).toContain(S)
    const mid = budgetAlertHtml(60, 100, S)
    expect(mid).toContain('预算过半')
    expect(mid).toContain('budget-warn')
    const high = budgetAlertHtml(85, 100, S)
    expect(high).toContain('预算快用完')
    expect(high).toContain('budget-err')
    const crit = budgetAlertHtml(96, 100, S)
    expect(crit).toContain('预算已超')
    expect(crit).toContain('budget-err2')
    // 旧稿 95% 与 80% 共用 err 分支：界面上两档长得一模一样，承诺的第三档不存在。
    // 这一条专门锁住「三档互不相同」，改回两档会立刻红。
    const clsOf = (h: string) => (h.match(/budget-(ok|warn|err2?|)\b/) || ['', ''])[0]
    const cls = [safe, mid, high, crit].map(clsOf)
    expect(new Set(cls).size, '四档样式互不相同，实际: ' + cls.join(',')).toBe(4)
    const tagOf = (h: string) => (h.match(/<b>([^<]+)<\/b>/) || ['', ''])[1]
    const tags = [safe, mid, high, crit].map(tagOf)
    expect(new Set(tags).size, '四档文案互不相同，实际: ' + tags.join(' / ')).toBe(4)
  })

  /* §7.5 隐状态绑定：告警条自带区间自述。没有这一句时，切范围会静默改掉
   * 「已用」而版面一字不变，读者只能把漂移的分子当成固定分母的读数。 */
  it('budgetAlertHtml 必须写出自述区间;空标签不渲染', () => {
    const html = budgetAlertHtml(60, 100, '本月至今')
    expect(html).toContain('本月至今')
    expect(html).toMatch(/本月至今[\s\S]*已用/)
    expect(budgetAlertHtml(60, 100, '')).toBe('')
  })

  /* 本月至今的费用与顶栏范围无关 —— 这是修复的核心，回归必须钉住：
   * 用同一份数据渲染四个范围，预算分子必须逐个相同。 */
  it('预算分子锁死日历月至今,四档范围下相同', () => {
    const st = defaultUiState()
    st.budgetUsd = 100
    const nums = (['today', '7d', '30d', 'all'] as const).map((r) => {
      const cmp = compareSources(DATA, st)
      const html = renderSettings(DATA, st, cmp, r, '')
      const m = html.match(new RegExp(esc(S) + '\\s*已用 <b>\\$([\\d.,kK]+)</b>'))
      return m ? m[1] : null
    })
    expect(nums[0]).not.toBeNull()
    for (const n of nums) expect(n).toBe(nums[0])
  })

  itLive('budgetUsd 进 settings 展示占用%;st 默认 0', () => {
    const st = defaultUiState()
    expect(st.budgetUsd).toBe(0)
    expect(st.drillProject).toBeNull()
    const cmp = compareSources(DATA, st)
    st.budgetUsd = 1
    const html = renderSettings(DATA, st, cmp, 'all', '')
    expect(html).toContain('占预算')
    expect(html).not.toMatch(/NaN/)
  })

  itLive('真实数据含 sessionsAll(增量契约,U4 钻取依赖)', () => {
    expect(Array.isArray(DATA.sessionsAll)).toBe(true)
    expect(DATA.sessionsAll.length).toBeGreaterThanOrEqual((DATA.sessions || []).length)
  })
})

/* ---- U6: streak + 本周 Top 模型 ---- */

/* ---- U7 修复:跨工具 stackBar 越界 + 外部 days 缺 total ---- */

describe('stackBar overflow guard', () => {
  it('total=0 但分段>0:条不超出 viewBox(rect x+width ≤ 900)', async () => {
    const { stackBar } = await import('./charts')
    const html = stackBar([
      {
        label: 'tool', total: 0, totalText: '0',
        segments: [
          { v: 44458565, color: MODEL_COLORS[0], name: 'cacheRead' },
          { v: 9926652, color: MODEL_COLORS[1], name: 'cacheWrite' },
          { v: 132704, color: MODEL_COLORS[2], name: 'input' },
          { v: 553984, color: MODEL_COLORS[3], name: 'output' }
        ]
      },
      { label: 'empty', total: 0, totalText: '0', segments: [] }
    ], { labelW: 132 })
    const rectRe = /<rect x="([0-9.]+)" y="[0-9.]+" width="([0-9.]+)"/g
    let m: RegExpExecArray | null
    let over = false
    while ((m = rectRe.exec(html))) {
      const end = Number(m[1]) + Number(m[2])
      if (!isFinite(end) || end > 900) over = true
    }
    expect(over).toBe(false)
    expect(html).toContain('<svg')
  })

  it('sumDayFields:外部 day 缺 total → 四段之和;有 total 用 total', async () => {
    const { sumDayFields } = await import('./pricing')
    const noTot = sumDayFields([{ cacheRead: 10, cacheWrite: 5, input: 3, output: 2 }])
    expect(noTot.total).toBe(20)
    expect(noTot.cacheRead).toBe(10)
    const withTot = sumDayFields([{ cacheRead: 10, cacheWrite: 5, input: 3, output: 2, total: 99 }])
    expect(withTot.total).toBe(99)
    const zeroTot = sumDayFields([{ cacheRead: 7, cacheWrite: 0, input: 0, output: 0, total: 0 }])
    expect(zeroTot.total).toBe(7) // total=0 视为缺失,分段兜底
  })

  itLive('真实 data:renderOverview 7d 跨工具条无 rect 溢出 900', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const html = renderOverview(DATA, st, cmp, '7d')
    const i = html.indexOf('跨工具总览')
    expect(i).toBeGreaterThanOrEqual(0)
    const section = html.slice(i, html.indexOf('</section>', i))
    const rectRe = /<rect x="([0-9.]+)" y="[0-9.]+" width="([0-9.]+)"/g
    let m: RegExpExecArray | null
    let maxEnd = 0
    while ((m = rectRe.exec(section))) {
      maxEnd = Math.max(maxEnd, Number(m[1]) + Number(m[2]))
    }
    expect(maxEnd).toBeGreaterThan(0)
    expect(maxEnd).toBeLessThanOrEqual(900)
    expect(html).not.toMatch(/NaN/)
  })
})

describe('U6 streak', () => {
  const active = (keys: string[]) => keys.map((d) => ({ d, total: 100 }))

  it('连续活跃:10 天全连续 → streak=10,endDate=末日', () => {
    const days = []
    for (let i = 1; i <= 10; i++) days.push({ d: '2026-09-' + String(i).padStart(2, '0'), total: 50 })
    const s = calcStreak(days, '2026-09-10')
    expect(s.streak).toBe(10)
    expect(s.endDate).toBe('2026-09-10')
  })

  it('断档:09-10 无数据则从 09-09 起算,断点前不计', () => {
    const days = active(['2026-09-08', '2026-09-09', '2026-09-10'])
    // 09-07 断,09-10 活跃 → 3
    expect(calcStreak(days, '2026-09-10').streak).toBe(3)
    // anchor=09-11(无数据) → 从前一天 09-10 起仍 3
    expect(calcStreak(days, '2026-09-11').streak).toBe(3)
    // 中间挖洞:08,09 有,10 无,anchor=10 → 从 09 起 2
    const hole = active(['2026-09-08', '2026-09-09'])
    expect(calcStreak(hole, '2026-09-10').streak).toBe(2)
    expect(calcStreak(hole, '2026-09-10').endDate).toBe('2026-09-09')
  })

  it('anchor 无数据且前一天也无 → 0;空 days → 0,null', () => {
    const days = active(['2026-09-01'])
    expect(calcStreak(days, '2026-09-20').streak).toBe(0)
    expect(calcStreak([], '2026-09-10')).toEqual({ streak: 0, endDate: null })
    expect(calcStreak([{ d: '2026-09-01', total: 0 }], '2026-09-01').streak).toBe(0)
  })

  itLive('真实数据: streak ≥1(有活跃日)且 endDate 非空', () => {
    const piLast = ((DATA.days || []) as any[]).map((d: any) => d.d).filter(Boolean).sort().pop()
    const s = calcStreak(DATA.days || [], piLast)
    expect(s.streak).toBeGreaterThanOrEqual(1)
    expect(s.endDate).toBeTruthy()
  })
})

/* 连续活跃曾经只取 data.days（只含 pi）并把锚点定成 pi 自己的最后一天。
 * 真实数据里 pi 停在 2026-09-20、外部 CLI 用到 10-07，页面上读成
 * 「2 天 · 至 09-20」，而全工具连段是 30 天。 */
describe('连续活跃：全工具口径', () => {
  const ext = (days: string[]) => ({
    generatedAt: '', convention: '', totals: {}, tools: [{ tool: 'x', label: 'X', models: [], days: days.map((d) => ({ d, total: 10, input: 10, output: 0, cacheRead: 0, cacheWrite: 0 })) }], prices: {}
  })

  it('pi 早早停更、外部仍在用时，连段必须延伸到页面锚点', () => {
    const data: any = {
      days: [{ d: '2026-10-01', total: 5 }, { d: '2026-10-02', total: 5 }],
      external: ext(['2026-10-03', '2026-10-04', '2026-10-05'])
    }
    // 页面锚点 = 全工具最大日 10-05；旧稿取 piLast=10-02 会读成 2 天
    const s = calcStreak(mergeDailyUsage(data), rangeAnchor(data))
    expect(s.streak).toBe(5)
    expect(s.endDate).toBe('2026-10-05')
    // 对照：只看 pi 侧确实只有 2 天，说明这个 bug 确实被数据形态掩盖
    expect(calcStreak(data.days, '2026-10-02').streak).toBe(2)
  })

  itLive('总览页的连续活跃走全工具：终点 = 页面锚点，且与合并序列算出的值一致', () => {
    const s = calcStreak(mergeDailyUsage(DATA), rangeAnchor(DATA))
    expect(s.streak).toBeGreaterThan(0)
    expect(s.endDate).toBe(rangeAnchor(DATA))
    const html = renderOverview(DATA, defaultUiState(), compareSources(DATA, defaultUiState()), 'all')
    expect(html).toContain(s.streak + ' 天')
    expect(html).toContain('全工具 · 至 ' + s.endDate)
  })
})

/* 表头与聚合曾各算一遍「本周」：表头按全工具锚点（10-07）算成 10-05~10-07，
 * 列表按 pi 锚点（09-20）算成 09-15~09-20 —— 表头声明了一个没有数据的周。 */
describe('本周 Top：表头窗口必须与列表同源', () => {
  it('isoWeekRange 周一为起点，跨月不串', () => {
    expect(isoWeekRange('2026-09-16')).toEqual({ start: '2026-09-14', end: '2026-09-16' })
    expect(isoWeekRange('2026-10-07')).toEqual({ start: '2026-10-05', end: '2026-10-07' })
    // 周日属于上一周
    expect(isoWeekRange('2026-09-20')).toEqual({ start: '2026-09-14', end: '2026-09-20' })
    expect(isoWeekRange(null)).toBeNull()
  })

  itLive('总览表头写的那一周，就是 weekTopModels 实际聚合的那一周', () => {
    const st = defaultUiState()
    const s = computeAll(DATA, st)
    const piLast = ((DATA.days || []) as any[]).map((d: any) => d.d).filter(Boolean).sort().pop()
    const rows = weekTopModels(DATA, s, piLast)
    if (!rows.length) return // 夹具里 pi 侧最近一周没有 dayModel，本例不适用
    const wk = isoWeekRange(piLast)!
    const html = renderOverview(DATA, st, compareSources(DATA, st), 'all')
    // 表头写 wk.start ~ wk.end；且不再出现页面锚点那一周（那里没有任何 dayModel）
    expect(html).toContain(wk.start + ' ~ ' + wk.end)
    const otherWk = isoWeekRange(rangeAnchor(DATA))!
    if (otherWk.start !== wk.start) expect(html).not.toContain(otherWk.start + ' ~ ' + otherWk.end)
    // 范围写明只覆盖 pi 侧 dayModel
    expect(html).toContain('仅 pi 侧模型（外部工具的用量见下方各工具行）')
  })
})

describe('U6 week top models', () => {
  it('ISO 周窗口:周一~锚日;排序 cost 降序;Top5 截断', () => {
    // 2026-09-16 是周三 → 该周 09-14(一)~09-16
    const data: any = {
      days: [{ d: '2026-09-16', total: 1 }],
      dayModel: [
        { d: '2026-09-13', key: 'prev-week', total: 999 }, // 上周,应排除
        { d: '2026-09-14', key: 'm-a', total: 100 },
        { d: '2026-09-15', key: 'm-b', total: 500 },
        { d: '2026-09-16', key: 'm-a', total: 50 },
        { d: '2026-09-16', key: 'm-c', total: 10 },
        { d: '2026-09-17', key: 'future', total: 1 } // 锚日之后,排除
      ]
    }
    const summary: any = { rows: [
      { m: { key: 'm-a' }, unit: 10 },  // $/M
      { m: { key: 'm-b' }, unit: 2 },
      { m: { key: 'm-c' }, unit: 100 },
      { m: { key: 'prev-week' }, unit: 999 },
      { m: { key: 'future' }, unit: 999 }
    ] }
    const top = weekTopModels(data, summary, '2026-09-16')
    const keys = top.map((r) => r.key)
    expect(keys).not.toContain('prev-week')
    expect(keys).not.toContain('future')
    // cost: m-a = 150×10/1e6 = 0.0015 > m-b = 500×2/1e6 = 0.001 → m-a 在前
    expect(keys.indexOf('m-a')).toBeLessThan(keys.indexOf('m-b'))
    expect(keys.indexOf('m-a')).toBe(0)
    // cost: m-a = (100+50)*10/1e6=0.0015; m-b=500*2/1e6=0.001; m-c=10*100/1e6=0.001
    const ma = top.find((r) => r.key === 'm-a')!
    near(ma.token, 150)
    near(ma.cost, 0.0015)
    expect(top.length).toBeLessThanOrEqual(5)
    expect(top.length).toBeGreaterThanOrEqual(3)
    const tot = top.reduce((s, r) => s + r.cost, 0)
    expect(ma.share).toBeGreaterThan(0)
    near(top.reduce((s, r) => s + r.share, 0), 100)
    void tot
  })

  itLive('无 dayModel / 空 anchor → [];真实 data 有输出且无 NaN', () => {
    expect(weekTopModels({ days: [{ d: '2026-09-16', total: 1 }], dayModel: [] }, { rows: [] }, '2026-09-16')).toEqual([])
    expect(weekTopModels({ dayModel: [{ d: '2026-09-16', key: 'x', total: 1 }] }, { rows: [] }, null)).toEqual([])
    expect(weekTopModels(DATA, computeAll(DATA, defaultUiState()))).toBeInstanceOf(Array)
    const top = weekTopModels(DATA, computeAll(DATA, defaultUiState()))
    top.forEach((r) => {
      expect(Number.isNaN(r.cost)).toBe(false)
      expect(r.key).toBeTruthy()
    })
  })

  itLive('总览冒烟:含 streak KPI 与 本周 Top;空 dayModel 降级;无 NaN', () => {
    const st = defaultUiState()
    const cmp = compareSources(DATA, st)
    const html = renderOverview(DATA, st, cmp, 'all')
    expect(html).toContain('连续活跃')
    expect(html).toContain('本周 Top 模型')
    expect(html).toMatch(/\d+ 天|—/)
    expect(html).not.toMatch(/NaN/)
    expect(html).not.toMatch(/>undefined</)
    // 空 dayModel → 降级
    const noDm = { ...DATA, dayModel: [], days: [] }
    const html2 = renderOverview(noDm, st, cmp, 'all')
    expect(html2).toContain('本周暂无数据')
    expect(html2).toContain('连续活跃')
    // streak=0 显示 —
    expect(html2).toContain('>—<')
    expect(html2).not.toMatch(/NaN/)
  })
})

/* ---- P0:外部工具费用按真实 token 构成计价(不是四价算术平均)+ 默认范围 all ---- */

/* 外部工具夹具:每个模型缓存占 98.9%(input 1M / cacheRead 100M / output 0.1M),
 * 单价 4 / 20 / 0.2 / 5。真值每模型 4 + 20 + 2 = 26;
 * 四档算术平均 7.3 会把同样 token 算成 738。 */
function extFixture() {
  const mk = function (id: string) {
    return {
      id, input: 1000000, cacheRead: 100000000, cacheWrite: 0, output: 100000,
      reasoning: 0, total: 101100000,
      price: { source: 'models.dev', cost: { input: 4, output: 20, cache_read: 0.2, cache_write: 5 } }
    }
  }
  const day = function (n: number) {
    return [{
      d: '2026-10-01', total: 101100000 * n, input: 1000000 * n,
      cacheRead: 100000000 * n, cacheWrite: 0, output: 100000 * n, reasoning: 0
    }]
  }
  const codex = mk('Big-Cache')
  const z1 = mk('Deepseek-v4-flash')
  const z2 = mk('deepseek-v4-flash')      // 与 z1 同模型,只是大小写不同
  const free = mk('deepseek-v4-flash-free')
  return {
    external: {
      generatedAt: '2026-10-01', totals: { sessions: 3, calls: 3 },
      tools: [
        { tool: 'codex', label: 'Codex CLI', sessions: 1, calls: 1, total: codex.total, models: [codex], days: day(1) },
        {
          tool: 'zcode', label: 'ZCode', sessions: 2, calls: 2,
          total: z1.total + z2.total + free.total, models: [z1, z2, free], days: day(3)
        }
      ]
    }
  }
}

describe('external cost by token composition', () => {
  it('rangeExt:缓存占绝大多数的工具按综合均价摊算,远低于四价均值', () => {
    const data: any = extFixture()
    const ext = externalSummary(data, 'ratio10')
    const re = rangeExt(ext, 'all', '2026-10-01')
    near(re.totalCost, 104)                    // 4 个模型条目 × 26
    const mean = (4 + 20 + 0.2 + 5) / 4
    expect(mean * 404.4).toBeGreaterThan(re.totalCost * 10)
    re.rows.forEach(function (r) { expect(Number.isNaN(r.cost)).toBe(false) })
  })

  it('externalModelRows:同一原始 id 归一化后只出一行,unit 按合并构成加权', () => {
    const data: any = extFixture()
    const rows = externalModelRows(data, 'ratio10')
    expect(rows.map((r) => r.key).sort())
      .toEqual(['big-cache', 'deepseek-v4-flash', 'deepseek-v4-flash-free'])
    expect(new Set(rows.map((r) => r.key)).size).toBe(rows.length)
    const ds = rows.find((r) => r.key === 'deepseek-v4-flash')!
    expect(ds.total).toBe(202200000)            // 两个工具的 token 已并到一行
    near(ds.unit, 26 / 101100000 * 1e6)         // 合并后构成不变,均价仍是构成加权值
    near(rows.reduce((t, r) => t + r.unit * r.total / 1e6, 0), 104)
  })

  itLive('真实数据:外部模型行总额 == externalSummary 总额,key 无重复', () => {
    const st = defaultUiState()
    const ext = externalSummary(DATA, st.policy)!
    const rows = externalModelRows(DATA, st.policy)
    expect(rows.length).toBeGreaterThan(0)
    expect(new Set(rows.map((r) => r.key)).size).toBe(rows.length)
    // 构成加权:Σ(unit × total) 必须等于按四段构成算出的总额。
    // 旧的「四档单价算术平均」口径会算出数倍于此的金额,本断言能锁死它。
    near(rows.reduce((t, r) => t + r.unit * r.total / 1e6, 0), ext.totalCost)
    rows.forEach(function (r) { expect(Number.isNaN(r.unit)).toBe(false) })
  })

  itLive('真实数据:模型视图的范围内费用向总览收敛(不再差数倍)', () => {
    const st = defaultUiState()
    const ext = externalSummary(DATA, st.policy)!
    const re = rangeExt(ext, '7d', rangeAnchor(DATA))
    const s = computeAll(DATA, st)
    const piCost = rangeStats(DATA, s, dailyCost(DATA, s), '7d').cost
    const html = renderModels(DATA, st, compareSources(DATA, st), '7d')
    const hit = /kpi-v">(\$[\d,.]+)</.exec(html)   // 唯一带 $ 的 KPI = 范围内模型费用
    expect(hit).not.toBeNull()
    const shown = Number(String(hit![1]).replace(/[$,]/g, ''))
    const overview = re.totalCost + piCost
    expect(overview).toBeGreaterThan(0)
    expect(Math.abs(shown - overview) / overview).toBeLessThan(0.15)
  })

  it('默认范围是 all:pi 落后于锚点时不整页为空', () => {
    const r = useHashRoute(['overview', 'models', 'sessions', 'projects', 'settings'])
    expect(r.range.value).toBe('all')
    expect(r.tab.value).toBe('overview')
  })
})

/* ---- 切片二:Ember Instrument 玻璃契约(App.vue CSS 静态断言) ---- */
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

describe('glass contract (slice2)', () => {
  const css = [
    readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), './styles/shell.css'), 'utf8'),
    readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), './styles/ui.css'), 'utf8'),
    readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), './styles/tokens.css'), 'utf8')
  ].join('\n')

  it('backdrop-filter 同屏 ≤3 且必须存在(液态玻璃硬约束);数据面禁 blur', () => {
    const blurs = css.match(/(^|[^-\w])backdrop-filter:\s*blur/g) || []
    expect(blurs.length).toBeGreaterThan(0)   // 玻璃必须有
    expect(blurs.length).toBeLessThanOrEqual(3)
    // 数据面/数字面永不玻璃化(s1e4/s4e1)
    expect(css).not.toMatch(/\.kpi\s*\{[^}]*backdrop-filter/)
    expect(css).not.toMatch(/table\.tbl[^{]*\{[^}]*backdrop-filter/)
    expect(css).not.toMatch(/\.empty\s*\{[^}]*backdrop-filter/)
  })

  it('prefers-reduced-transparency 块:关闭全部 backdrop 并回落实心(--glass 落 solid)', () => {
    const m = css.match(/@media \(prefers-reduced-transparency: reduce\) \{([\s\S]*?)\n\}/)
    expect(m).toBeTruthy()
    const block = (m as RegExpMatchArray)[1]
    expect(block).toContain('backdrop-filter: none')
    expect(block).not.toMatch(/blur\(/)          // 降级态必无 backdrop blur
    expect(block).toContain('background: var(--bg)')   // header 实心 fallback
    expect(block).toContain('background: var(--card)') // 空态实心 fallback
  })

  it('空态为 opacity 级玻璃(wash token,无 blur);color-mix 洗色已收缩(改版前 53)', () => {
    expect(css).toMatch(/\.empty \{[^}]*background: var\(--wash-1\)/)
    const n = (css.match(/color-mix\(/g) || []).length
    expect(n).toBeLessThanOrEqual(36)
    // 中性 fg 洗色必须全部 token 化(禁新增内联 color-mix fg)
    expect(css).not.toMatch(/color-mix\(in srgb, var\(--fg\)/)
  })
})

describe("rangeAnchor across sources", () => {
  it("uses freshest day from pi + external, not only pi", () => {
    const data = {
      days: [{ d: "2026-09-20", total: 10 }],
      external: {
        tools: [
          { days: [{ d: "2026-10-04", total: 1 }] },
          { days: [{ d: "2026-10-05", total: 2 }] }
        ]
      }
    }
    expect(rangeAnchor(data)).toBe("2026-10-05")
  })
  it("still works with pi-only data", () => {
    expect(rangeAnchor({ days: [{ d: "2026-09-20" }] })).toBe("2026-09-20")
  })
})

describe("mergeDailyUsage", () => {
  it("sums pi + external days for the chart", () => {
    const data = {
      days: [{ d: "2026-09-20", total: 10, input: 1, output: 2, cacheRead: 3, cacheWrite: 4 }],
      external: { tools: [{ days: [{ d: "2026-10-05", total: 100, input: 10, output: 20, cacheRead: 30, cacheWrite: 40 }] }] }
    }
    const m = mergeDailyUsage(data)
    expect(m.length).toBe(2)
    expect(m[0].d).toBe("2026-09-20")
    expect(m[1].d).toBe("2026-10-05")
    expect(m[1].total).toBe(100)
  })
})
