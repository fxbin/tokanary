/* 渲染层回归测试：整页构造器存在性 + 本轮修复的口径锁。
 * 用法: npx vitest run (web/ 目录下)。需要 .cache/dashboard.json（tokanary refresh 生成） */
import { describe, it, expect } from 'vitest'
import {
  computeAll, compareSources, money, dailyCost, externalSummary, fmt,
  rangeStats, rangeExt, rangeAnchor, withMergedDays, mergeDailyCost, RANGES,
  type RangeKey
} from './pricing'
import {
  renderOverview, renderProjects, renderSessions, renderSettings, renderModels,
  defaultUiState, esc, type UiState
} from './render'
import { hasAnyPricing, loadDashboardFixture } from './testsupport'
import { windowUsage, USAGE_WINDOWS } from './usage'

const DATA = loadDashboardFixture()
const suite = DATA ? describe : describe.skip

const st = defaultUiState()
const cmp = DATA ? compareSources(DATA, st) : {}


function overview(range: RangeKey, ui?: Partial<UiState>): string {
  const s = Object.assign(defaultUiState(), ui || {})
  return renderOverview(DATA, s, cmp, range)
}

/** 全部 Tab 的渲染结果拼在一起 —— 页面由 App.vue 分 Tab 装配，
 *  所以「整页」冒烟就是把五个渲染器的输出接起来。 */
function allTabs(data: any, ui?: Partial<UiState>, range: RangeKey = 'all'): string {
  const s = Object.assign(defaultUiState(), ui || {})
  const c = compareSources(data, s)
  return [
    renderOverview(data, s, c, range),
    renderModels(data, s, c, range),
    renderSessions(data, s, c, range),
    renderProjects(data, s, c, range),
    renderSettings(data, s, c, range, '')
  ].join('\n')
}

/** 项目视图钻取到某项目时的产出表（0 提交仓库折进 details，不在首层）。 */
function projectsHtml(drill: string | null, ui?: Partial<UiState>): string {
  const s = Object.assign(defaultUiState(), ui || {})
  if (drill) s.drillProject = drill
  return renderProjects(DATA, s, cmp, 'all')
}

/** 全部 Tab 的区块冒烟：返回缺失的区块标记。 */
function missingSections(html: string, data: any, range: RangeKey): string[] {
  // Markers are the <h2> titles the section renderers actually emit today.
  // They drifted once with the old whole-page renderer, which turned five of
  // these checks into no-ops - hence the range-aware session title and the
  // hint-qualified ones, so a word appearing in prose cannot satisfy them.
  const markers = [
    '模型 Token 构成', '模型费用占比', '模型明细与单价',
    '<h2>洞察', '本周 Top 模型', '时段热力图', '跨工具总览', '项目归因',
    '>' + RANGES[range].label + ' 会话 ', '价格设置',
    'data-field="cache_read"', '<svg'
  ]
  // The per-model channel dropdown only exists when a model actually offers
  // more than one channel. With no price table (a fresh clone ships none -
  // .cache/prices-raw.json is generated locally) there is nothing to switch
  // between, so requiring the marker would report a section that was never
  // applicable. Pass `data` to make the check honest; omit it to demand it.
  if (!data || hasVariants(data)) markers.push('data-variant=')
  const miss: string[] = []
  markers.forEach(function (m) { if (html.indexOf(m) < 0) miss.push(m) })
  return miss
}

/** True when at least one priced model offers a channel choice. */
function hasVariants(data: any): boolean {
  const models = ((data || {}).models || []) as any[]
  return models.some(function (m) {
    const meta = ((data.pricing || {})[m.key] || {}) as any
    const vs = meta.variants || []
    return vs.length > 1
  })
}

suite('全部分块：冒烟与契约', () => {
  it('各 Tab 的区块齐全 + 输入框 + 渠道下拉 + SVG', () => {
    expect(missingSections(allTabs(DATA, undefined, 'all'), DATA, 'all')).toEqual([])
  })

  it('KPI 总额 == 逻辑层总额', () => {
    const total = money(computeAll(DATA, st).totalCost)
    expect(allTabs(DATA, undefined, 'all')).toContain(total)
  })

  it('无 undefined/NaN 泄漏', () => {
    const html = allTabs(DATA, undefined, 'all')
    expect(html).not.toMatch(/>undefined</)
    expect(html).not.toMatch(/NaN/)
  })

  it('非 models.dev 来源仍标出来(调用方传入自定义单价时不冒充 models.dev)', () => {
    const ui: Partial<UiState> = {}
    const s = defaultUiState()
    s.customPrices = { 'gpt-5.6-sol': { input: 1, output: 2, cache_read: 0.1, cache_write: 0.2 } }
    Object.assign(ui, s)
    expect(allTabs(DATA, ui, 'all')).toContain('自定义源')
  })

  it('缺 data 时给出生效提示而非抛错', () => {
    const html = renderOverview(null, st, cmp, 'all')
    expect(html).toContain('tokanary refresh')
    // 提示只能指向还存在的产物。断言某个已退场的文件名等于永远通过，
    // 所以断言这一类：任何生成器内部痕迹都不该出现在用户界面里。
    expect(html).not.toMatch(/\.py\b|data\.js/)
  })

  it('缺 gateway 段时降级到 models.dev 而非抛错', () => {
    const noGw: any = JSON.parse(JSON.stringify(DATA))
    noGw.gateway = null
    const html = renderOverview(noGw, defaultUiState(), compareSources(noGw, defaultUiState()), 'all')
    expect(html).not.toContain('>undefined<')
    expect(html).not.toMatch(/NaN/)
    expect(html).toContain('计费口径')
  })

  it('全新克隆(无任何价格表)仍渲染完整且区块齐全', () => {
    // This is what a fresh clone looks like: .cache/prices-raw.json is
    // gitignored, so `tokanary prices` has not been run yet. Token totals must
    // still be right and every section must still render - only the money
    // collapses to zero.
    const bare: any = JSON.parse(JSON.stringify(DATA))
    bare.pricing = {}
    bare.gateway = null
    for (const m of bare.models || []) { m.cost = null }
    for (const t of ((bare.external || {}).tools || [])) {
      for (const k of Object.keys(t.models || {})) t.models[k].price = { source: 'unpriced' }
    }
    expect(hasAnyPricing(bare)).toBe(false)
    const html = allTabs(bare, undefined, 'all')
    expect(missingSections(html, bare, 'all')).toEqual([])
    expect(html).not.toContain('>undefined<')
    expect(html).not.toMatch(/NaN/)
    expect(html).toContain('0')
  })
})

/* ------------------------------------------------------------------ P0：费用口径 */

suite('总览：同一笔钱不得加自己', () => {
  it('不再出现与「{范围}费用」重复的「{范围}全部工具」KPI', () => {
    for (const range of ['all', '30d', '7d'] as RangeKey[]) {
      const html = overview(range)
      // 断言收窄到 KPI 条这一段：它防的是「同屏两个总额」，具体形态就是顶部
      // 多出一格重复的钱。整页一刀切会连带禁止图注里的来源自述 —— 而那正是
      // §7.5 要求的「这个数是谁的子集」。护栏该守的地方要守住，不该把正文
      // 也一并封掉。
      const kpis = html.match(/<section class="kpis">[\s\S]*?<\/section>/)
      expect(kpis).not.toBeNull()
      expect(kpis![0]).not.toContain('全部工具')
      expect(kpis![0]).not.toContain(RANGES[range].label + '全部工具')
    }
  })

  it('不再出现「pi 占」这种百分比占比（合并序列下它恒自指，无法诚实成立）', () => {
    for (const range of ['all', '30d', '7d'] as RangeKey[]) {
      expect(overview(range)).not.toContain('pi 占')
    }
  })

  it('pi + 外部 === 合计，且旧 bug 的翻倍值不上页', () => {
    const s = computeAll(DATA, st)
    const ext = externalSummary(DATA, st.policy)
    const piByDay = dailyCost(DATA, s)
    const merged = withMergedDays(DATA)
    const anchor = rangeAnchor(DATA)
    for (const range of ['all', '30d', '7d'] as RangeKey[]) {
      // rs 吃的是 withMergedDays()（已合入外部工具），所以它已经是全工具合计；
      // 旧稿在这之上再加一次 rangeExt().totalCost，就是同一笔钱加自己。
      const rs = rangeStats(merged, s, mergeDailyCost(piByDay, ext, DATA), range)
      const pi = rangeStats(DATA, s, piByDay, range).cost
      const extCost = rangeExt(ext, range, anchor).totalCost
      // 浮点求和顺序不同，容差 1e-6（实测最大残差 1.5e-11，即 0 分钱）。
      expect(Math.abs(pi + extCost - rs.cost)).toBeLessThan(1e-6)
      expect(overview(range)).not.toContain(money(rs.cost + extCost))
    }
  })

  it('首格费用把 pi 与外部两个绝对值写进 sub', () => {
    const s = computeAll(DATA, st)
    const ext = externalSummary(DATA, st.policy)
    const pi = rangeStats(DATA, s, dailyCost(DATA, s), 'all').cost
    const extCost = rangeExt(ext, 'all', rangeAnchor(DATA)).totalCost
    const nTools = (ext as any).rows.length
    expect(overview('all')).toContain('pi ' + money(pi) + ' + 其他 ' + nTools + ' 个工具 ' + money(extCost))
  })

  it('没有外部数据时退回单一口径，不出 NaN / 空串', () => {
    const bare: any = { ...DATA, external: null }
    const html = renderOverview(bare, defaultUiState(), cmp, 'all')
    expect(html).not.toMatch(/NaN/)
    expect(html).not.toContain('外部 $')
    expect(html).not.toContain('其他 ')
    expect(html).toContain('models.dev 单价')
  })
})

suite('总览：跨工具总览每行只报自己那一份用量', () => {
  /** 抓堆叠条里每一行的 sub（「<token> · $<cost>」）。 */
  function rowsOf(range: RangeKey): string[] {
    const out: string[] = []
    const re = /class="lbl-sub">([^<]+) · /g
    let m: RegExpExecArray | null
    while ((m = re.exec(overview(range))) !== null) out.push(m[1])
    return out
  }

  it('pi 行的数是 pi 自己的量，不是全工具合计', () => {
    const s = computeAll(DATA, st)
    const piByDay = dailyCost(DATA, s)
    for (const range of ['all', '30d', '7d'] as RangeKey[]) {
      const piRs = rangeStats(DATA, s, piByDay, range)
      const html = overview(range)
      if (piRs.fields.total <= 0) continue // pi 该范围内没用量，本就不该有这行
      const row = html.match(/class="lbl">pi<\/text><text[^>]*class="lbl-sub">([^<]+)/)
      expect(row, 'pi 行应存在: ' + range).toBeTruthy()
      expect(row![1], 'pi 行数值: ' + range).toContain(fmt(piRs.fields.total))
    }
  })

  it('跨工具总览不出现全工具合计（那是各行之和，不是其中一行的量）', () => {
    const s = computeAll(DATA, st)
    const ext = externalSummary(DATA, st.policy)
    const allRs = rangeStats(withMergedDays(DATA), s, mergeDailyCost(dailyCost(DATA, s), ext, DATA), '7d')
    // 前置条件：近 7 天确有外部工具用量，全工具合计是个像样的数，断言才有意义
    expect(allRs.fields.total).toBeGreaterThan(0)
    const subs = rowsOf('7d').join(' | ')
    expect(subs).not.toContain(fmt(allRs.fields.total))
  })

  it('pi 行只在 pi 自身于范围内有用量时出现（与外部行同一把尺子）', () => {
    const s = computeAll(DATA, st)
    const piByDay = dailyCost(DATA, s)
    for (const range of ['all', '30d', '7d'] as RangeKey[]) {
      const piTotal = rangeStats(DATA, s, piByDay, range).fields.total
      const shown = /class="lbl">pi<\/text>/.test(overview(range))
      expect(shown, 'range=' + range + ' piTotal=' + piTotal).toBe(piTotal > 0)
    }
  })

  it('各行之和不超过全工具合计（同一笔钱不得在两行各算一次）', () => {
    const s = computeAll(DATA, st)
    const ext = externalSummary(DATA, st.policy)
    for (const range of ['all', '30d', '7d'] as RangeKey[]) {
      const allRs = rangeStats(withMergedDays(DATA), s, mergeDailyCost(dailyCost(DATA, s), ext, DATA), range)
      const shown = rowsOf(range)
        .map(function (t) { return parseTokenText(t) })
        .reduce(function (a, b) { return a + b }, 0)
      // 外部工具的 per-day 与 per-model 在采集层就对不齐（Codex 差 4.14%），
      // 所以允许一点残差，但绝不能出现「全工具再多算一遍」那种量级的虚高。
      expect(shown, 'range=' + range).toBeLessThanOrEqual(allRs.fields.total * 1.05)
    }
  })
})

/** 把 '610.23M' / '3.56B' / '624.20M' 这类 fmt 输出还原成数值。 */
function parseTokenText(t: string): number {
  const m = t.trim().match(/^([\d.]+)([KMB]?)$/)
  if (!m) return 0
  const mult = m[2] === 'K' ? 1e3 : m[2] === 'M' ? 1e6 : m[2] === 'B' ? 1e9 : 1
  return parseFloat(m[1]) * mult
}

/* ------------------------------------------------------------------ P2：读数条排版 */

suite('总览：读数条不留空行', () => {
  it('6 格 KPI + 走势行内的「连续活跃」注记，不产生落单巨型格', () => {
    // 7 个 .kpi 在 1196px 内容宽下按 180px 基准只排得下 6 个，第 7 个会被 flex-grow
    // 拉成整行宽的空块。回归锁：走势行之前只准有 6 个格，连续活跃必须落在 spark-row 里。
    const html = overview('all')
    const head = html.slice(0, html.indexOf('spark-row'))
    expect((head.match(/class="kpi"/g) || []).length).toBe(6)
    expect(html).toContain('class="kpi kpi-note"')
    expect(html.indexOf('连续活跃')).toBeGreaterThan(html.indexOf('spark-row'))
  })

  it('sparkline 不再只有 tooltip，有一句文字标签', () => {
    expect(overview('all')).toContain('逐日 token')
  })
})

/* ------------------------------------------------------------------ 窗口用量 */

suite('总览：窗口用量（不再假装知道额度）', () => {
  it('只报实测窗口用量，不出现任何百分比或额度上限', () => {
    // 旧稿的分母是用户自填的上限，612.35M tok 除以它得到的百分比不能支撑任何决策。
    // 各家额度在服务端，本机读不到，所以这一块不给占比。
    // 断言必须卡在 w-val 内部：整页别处（如本周 Top）本来就合法地有百分比。
    const html = overview('all')
    expect(html).toContain('窗口用量')
    expect(html).not.toContain('额度上限未设置')
    expect(html).not.toContain('q-pct')
    expect(html).not.toMatch(/<span class="w-val">[^<]*%/)
  })

  it('把「这是用量不是额度」和费用口径写在明面上', () => {
    const html = overview('all')
    expect(html).toContain('不是各家订阅额度')
    expect(html).toContain('API 等价')
    expect(html).toContain('不随上方范围切换')
    // 两个窗口嵌套、并排列会诱导相减；这一点必须在界面上点破
    expect(html).toContain('两个窗口是嵌套的')
  })

  it('窗口标签只给算得出来的：近 7 天 / 近 30 天，不出现 5 小时', () => {
    // 外部 CLI 的用量日志是逐日粒度（pi 侧才有 hours），5 小时窗口测不出来就不提供。
    const html = overview('all')
    expect(html).toContain('近 7 天')
    expect(html).toContain('近 30 天')
    expect(html).not.toContain('5 小时')
    expect(html).not.toContain('5h')
  })

  it('窗口标签只出现在表头一次，不再每行重复', () => {
    // 旧稿每个工具重复「近 7 天 / 近 30 天」两个标签且不对齐，只能横着读
    const html = overview('all')
    expect((html.match(/class="win-h/g) || []).length).toBe(4)
    expect(html).toContain('>近 7 天<')
    expect(html).toContain('>近 30 天<')
    // 金额列表头必须写明是 API 等价，不能读成实际账单
    expect(html).toContain('近 7 天 API 等价 $')
  })

  it('某窗口无用量时写「无用量」占位，不省略整格', () => {
    // 省略整格会让换行后下一格的数贴到上一个工具身上。首屏只列前 5 个工具，
    // 被截掉的要另计。
    const u = windowUsage(DATA, st.policy)
    const shown = u.slice(0, 5)
    const zero7d = shown.filter((x) => x.byWindow['7d'].tokens === 0)
    const html = overview('all')
    // 近 7 天 token 格与近 7 天金额格各一个「无用量」；近 30 天格有值
    expect((html.match(/class="win-none">无用量<\/div>/g) || []).length).toBe(zero7d.length * 2)
    if (u.length > shown.length) expect(html).toContain('另有 ' + (u.length - shown.length) + ' 个工具')
  })

  it('工具行按近 30 天 token 降序，且每个工具带窗口标签与 token/金额', () => {
    const u = windowUsage(DATA, st.policy)
    expect(u.length).toBeGreaterThan(1)
    for (let i = 1; i < u.length; i++) expect(u[i].tokens).toBeLessThanOrEqual(u[i - 1].tokens)
    const html = overview('all')
    expect(html).toContain(esc(u[0].label))
  })
})

/* ------------------------------------------------------------------ P1：设置页 */

suite('设置页：单价只有 models.dev', () => {
  it('不再渲染自定义价格源与网关价目表', () => {
    const html = renderSettings(DATA, st, cmp, 'all', '')
    expect(html).not.toContain('custom-url')
    expect(html).not.toContain('自定义价格源')
    expect(html).not.toContain('网关价目表')
    expect(html).not.toContain('自有网关')
    expect(html).toContain('models.dev')
  })

  it('不再出现「切换后全页金额重算」这类兑现不了的说明', () => {
    expect(renderSettings(DATA, st, cmp, 'all', '')).not.toContain('切换后全页金额重算')
  })

  it('「单价来源」与「价格来源」合并成一行，信息一条不少', () => {
    // 单价只有一个源，两个标签说的是同一件事。合并后金额、抓取时间、匹配情况
    // 都必须还在同一格里，少一条就等于用整洁换掉了信息。
    const html = renderSettings(DATA, st, cmp, 'all', '')
    expect(html).toContain('单价来源')
    expect(html).not.toContain('价格来源：')
    expect(html).toContain('抓取于')
    expect(html).toMatch(/个模型里 \d+ 个已匹配单价/)
  })

  it('保留手动改价的导入/导出入口', () => {
    const html = renderSettings(DATA, st, cmp, 'all', '')
    expect(html).toContain('id="btn-export"')
    expect(html).toContain('id="btn-import"')
    expect(html).toContain('id="io"')
    expect(html).toContain('手动改价')
  })

  it('不再有额度输入：额度在服务端，本机设不了就不给一个假的上限框', () => {
    const html = renderSettings(DATA, st, cmp, 'all', '')
    expect(html).not.toContain('quota-tok-')
    expect(html).not.toContain('quota-usd-')
    expect(html).not.toContain('tokanary-quotas-v1')
    // 本机工具的花费提醒由月预算承担，那条入口必须还在
    expect(html).toContain('id="budget-usd"')
  })
})

/* ------------------------------------------------------------------ P1：产出 git */

suite('产出 · git：已从总览下沉到项目视图', () => {
  it('总览不再渲染 git 提交表', () => {
    expect(overview('all')).not.toContain('产出 · git')
  })

  it('项目视图有产出表，未归因的显式标出、不按 0 冒充', () => {
    const html = projectsHtml(null)
    expect(html).toContain('产出 · git')
    expect(html).toContain('未归因')
    expect(html).not.toMatch(/NaN/)
  })

  it('同名钻取时才摊「每次提交成本」，数值 = 项目费用 ÷ 提交数', () => {
    const s = computeAll(DATA, st)
    const unitByKey: Record<string, number> = {}
    s.rows.forEach((r: any) => { unitByKey[r.m.key] = r.unit })
    const allSess: any[] = (DATA.sessionsAll && DATA.sessionsAll.length) ? DATA.sessionsAll : (DATA.sessions || [])
    const matched = (DATA.projects || [])
      .map((p: any) => p.name as string)
      .find((n: string) => (DATA.yield || []).some((y: any) => y.project === n && y.total > 0))
    expect(matched, '夹具里应至少有一个仓库名与项目名同名的可归因项目').toBeTruthy()
    const pCost = allSess
      .filter((x) => (x.project || '(无项目)') === matched)
      .reduce((t, x) => t + (unitByKey[x.model] || 0) * (x.total || 0) / 1e6, 0)
    const commits = (DATA.yield || [])
      .filter((y: any) => y.project === matched)
      .reduce((t: number, y: any) => t + (y.total || 0), 0)
    const html = projectsHtml(matched)
    expect(html).toContain('该项目产出 · git')
    expect(html).not.toContain('无提交，无法摊算')
    expect(html).toContain(money(pCost / commits))
  })

  it('0 提交的仓库不进首层表，折进 details', () => {
    const zeros = (DATA.yield || []).filter((y: any) => !(y.total > 0)).length
    if (!zeros) return // 夹具里没有 0 提交的仓库，本例不适用
    const html = projectsHtml(null)
    expect(html).toContain('<details class="yield-zero">')
    expect(html).toContain('没有提交')
  })
})

/* ------------------------------------------------------------------ P1：脏字节 */

suite('esc():坏字节不外泄', () => {
  it('U+FFFD 替换字符先清掉再转义', () => {
    expect(esc('核对�…')).toBe('核对…')
    expect(esc('a�<b>')).toBe('a&lt;b&gt;')
    expect(esc(null)).toBe('')
  })

  it('提交 subject 里的坏字节不会出现在项目页 HTML 里', () => {
    expect(projectsHtml(null)).not.toContain('�')
  })
})

/* ------------------------------------------------------------------ P1：空态 */

suite('空态：区分「从来没有数据」与「落在窗口外」', () => {
  function sessionsHtml(range: RangeKey, data: any): string {
    return renderSessions(data, defaultUiState(), compareSources(data, defaultUiState()), range)
  }

  it('窗口内为 0 而全量有数据 → 说清在窗口外并给出「切到全部」', () => {
    const html = sessionsHtml('7d', DATA)
    if (!html.includes('class="empty"')) return // 该夹具窗口内本来就有会话，不适用
    expect(html).toContain('落在窗口外')
    expect(html).toContain('切到「全部」')
  })

  it('仓库里一条会话都没有 → 指向采集，而不是让用户改范围', () => {
    const empty: any = { ...DATA, sessions: [], sessionsAll: [] }
    const html = sessionsHtml('7d', empty)
    expect(html).toContain('tokanary collect')
    expect(html).toContain('也没有任何记录')
  })

  // 模型页曾单独写死一句「换更大时间范围试试」，于是「仓库没数据」也被指去改范围。
  function modelsHtml(range: RangeKey, data: any): string {
    const s = defaultUiState()
    return renderModels(data, s, compareSources(data, s), range)
  }

  it('模型页同例：pi 侧日明细落后于锚点 → 说清落在窗口外', () => {
    // 复刻真实现象：pi 侧 dayModel 最新 2026-09-20，锚点 2026-10-07，
    // 近 7 天窗口里的模型 token 为 0 —— 那是「数据在窗口外」，不是没数据。
    const lagged: any = JSON.parse(JSON.stringify(DATA))
    lagged.external = null
    lagged.dayModel = (DATA.dayModel || []).filter((dm: any) => dm.d <= '2026-09-20')
    const html = modelsHtml('7d', lagged)
    if (!html.includes('class="empty"')) return // 该夹具窗口内本来就有模型用量，不适用
    expect(html).toContain('落在窗口外')
    expect(html).toContain('切到「全部」')
  })

  it('模型页同例：一个模型都没有 → 指向采集', () => {
    const empty: any = JSON.parse(JSON.stringify(DATA))
    empty.external = null
    empty.models = []; empty.days = []; empty.dayModel = []
    expect(modelsHtml('7d', empty)).toContain('tokanary collect')
  })
})

/* ------------------------------------------------------------------ 窗口用量口径（usage.ts）

   下面这几条只用合成夹具，不依赖 .cache/dashboard.json —— 它们守的是这段代码本身。
   背景：这段逻辑的前身 computeQuotas 单价曾退回「四档单价算术平均」，缓存读占大头的
   工具（真实 Codex：33,876,829,458 缓存读、0 缓存写）费用直接差 8 倍，而整个套件
   当时照样全绿。external.tools[] 从不带 unit 字段，所以那条 fallback 是唯一生效路径。 */

describe('usage：窗口用量按真实 token 构成加权', () => {
  /** 锚点日。windowUsage 走 filterDaysByRange(anchor = rangeAnchor)，窗口以最新数据日
   *  为终点向前推，所以夹具的 day 必须落在 rangeAnchor 能扫到的范围内。 */
  function anchorKey(): string {
    const d = new Date()
    const pad = function (x: number) { return String(x).padStart(2, '0') }
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate())
  }

  function dayOffset(offset: number): string {
    const t = new Date(new Date(anchorKey() + 'T00:00:00').getTime() + offset * 86400000)
    const pad = function (x: number) { return String(x).padStart(2, '0') }
    return t.getFullYear() + '-' + pad(t.getMonth() + 1) + '-' + pad(t.getDate())
  }

  /** 合成一个外部工具：单模型 + 指定几天的日用量。 */
  function extTool(tool: string, mix: { cacheRead: number; cacheWrite: number; input: number; output: number }, cost: any, dayOffsets: number[] = [0]): any {
    const total = mix.cacheRead + mix.cacheWrite + mix.input + mix.output
    const m = { id: 'demo-model', total, cacheRead: mix.cacheRead, cacheWrite: mix.cacheWrite, input: mix.input, output: mix.output }
    return {
      tool, label: tool, total,
      models: [Object.assign({}, m, { price: { cost } })],
      days: dayOffsets.map((o) => Object.assign({ d: dayOffset(o) }, mix, { total }))
    }
  }

  function dataOf(tool: any): any {
    return { external: { tools: [tool], totals: { sessions: 1, calls: 1 } } }
  }

  it('缓存读占 99% 的工具，费用必须远低于四价算术平均（锁加权均价，不锁字符串）', () => {
    const total = 1_000_000_000
    const mix = { cacheRead: 990_000_000, cacheWrite: 1_000_000, input: 5_000_000, output: 4_000_000 }
    const price = { input: 10, output: 30, cache_read: 0.1, cache_write: 12.5 }
    const u = windowUsage(dataOf(extTool('demo', mix, price)))[0]

    // 真实加权费用 = 990e6×0.1 + 5e6×10 + 4e6×30 + 1e6×12.5，除以 1e6 = 281.5
    expect(u.byWindow['30d'].tokens).toBe(total)
    expect(u.byWindow['30d'].cost).toBeCloseTo(281.5, 6)
    expect(u.byWindow['30d'].cost).toBeGreaterThan(0)

    // 旧口径：四档单价算术平均 (10+30+0.1+12.5)/4 = 13.15 $/M → 13,150
    const arithUnit = (10 + 30 + 0.1 + 12.5) / 4
    const arithCost = arithUnit * total / 1e6
    expect(u.byWindow['30d'].cost).toBeLessThan(arithCost * 0.05)
    expect(arithCost / u.byWindow['30d'].cost).toBeGreaterThan(40)
  })

  it('policy 参数真的进算式：缺 cache_write 单价时 ratio10 与 zero 差出那笔缓存写', () => {
    const mix = { cacheRead: 0, cacheWrite: 200_000_000, input: 600_000_000, output: 200_000_000 }
    // 故意不给 cache_write：normalizeCost 会按 policy 推算（ratio10 = input×1.25，zero = 0）
    const price = { input: 10, output: 30, cache_read: 0.1 }
    const data = dataOf(extTool('demo', mix, price))
    const a = windowUsage(data, 'ratio10')[0]
    const b = windowUsage(data, 'zero')[0]

    // 缓存写 200e6 × (12.5 - 0) / 1e6 = 2,500
    expect(a.byWindow['30d'].cost).toBeCloseTo(14_500, 6)
    expect(b.byWindow['30d'].cost).toBeCloseTo(12_000, 6)
    expect(a.byWindow['30d'].cost - b.byWindow['30d'].cost).toBeCloseTo(2_500, 6)
    expect(a.byWindow['30d'].cost).toBeGreaterThan(b.byWindow['30d'].cost)
    // 省略 policy 时必须落到 ratio10：默认值翻成 zero 也要被这条抓住
    expect(windowUsage(data)[0].byWindow['30d'].cost).toBeCloseTo(a.byWindow['30d'].cost, 6)
  })

  it('窗口边界正确：7 天窗口不含 7 天前，30 天窗口含 29 天前但不含 30 天前', () => {
    // 边界 = anchor-(n-1) .. anchor（rangeCutoffKey）。旧稿的 windowDays 另写了一套
    // 日算法，还把类型里的 '5h' 兜底成 30 天；这里锁住窗口与顶栏范围切换同源。
    const mix = { cacheRead: 0, cacheWrite: 0, input: 1000, output: 0 }
    const data = dataOf(extTool('demo', mix, { input: 1, output: 1 }, [0, -3, -6, -7, -29, -30]))
    const u = windowUsage(data)[0]
    expect(u.byWindow['7d'].tokens).toBe(3000)   // 0 / -3 / -6；-7 已在窗外
    expect(u.byWindow['30d'].tokens).toBe(5000)  // 加上 -7、-29；-30 已在窗外
  })

  it('只提供算得出来的窗口：USAGE_WINDOWS 里没有 5 小时', () => {
    // 外部 CLI 的用量日志是逐日粒度（pi 侧才有 hours），5 小时窗口测不出来就不提供。
    expect(USAGE_WINDOWS.map((w) => w.key)).toEqual(['7d', '30d'])
    expect(USAGE_WINDOWS.map((w) => w.label)).not.toContain('5 小时')
  })

  it('近 30 天没动静的工具不进列表', () => {
    const quiet: any = extTool('quiet', { cacheRead: 0, cacheWrite: 0, input: 10, output: 0 }, { input: 1, output: 1 }, [-45])
    const busy: any = extTool('busy', { cacheRead: 0, cacheWrite: 0, input: 20, output: 0 }, { input: 1, output: 1 }, [0])
    const u = windowUsage({ external: { tools: [quiet, busy], totals: {} } })
    expect(u.map((x) => x.tool)).toEqual(['busy'])
  })
})
