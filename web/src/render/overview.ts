import {
  fmt, money, pct, CATS, POLICIES, SOURCES,
  normalizeCost, costOfTokens, computeAll, compareSources,
  dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor, withMergedDays, mergeDailyCost,
  filterHoursByRange, hourMatrix, rangeInsights, RANGES,
  filterDaysByRange, rangeCutoffKey, calcStreak, weekTopModels, isoWeekRange, mergeDailyUsage,
  type PricingOpts, type PriceSource, type RangeKey
} from '../pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, heatmap, trimNum } from '../charts'
import { windowUsage, type WindowUsage } from '../usage'
import { th, sourceTag, stableColorIndex, sesRecency, pad2, localDay, esc, type UiState } from './shared'

export function renderOverview(data: any, st: UiState, cmp: Record<string, number>, range: RangeKey): string {
  if (!data) {
    return '<div class="empty">没有用量数据 —— 请先运行 <code>tokanary refresh</code> 重建仓库。</div>'
  }
  const s = computeAll(data, st)
  const ext = externalSummary(data, st.policy)
  const merged = withMergedDays(data)
  // pi 侧逐日费用（只喂 data.dayModel，即纯 pi）；合并后才是「全工具」。
  // 两个口径必须分开取：rs.cost 已经是全工具合计，再叠一次外部费用就是同一笔钱加自己。
  const piCostByDay = dailyCost(data, s)
  const costByDay = mergeDailyCost(piCostByDay, ext, data)
  const rs = rangeStats(merged, s, costByDay, range)
  const piRs = rangeStats(data, s, piCostByDay, range)
  const re = rangeExt(ext, range, rangeAnchor(data))
  const html: string[] = []
  const rangeLabel = (RANGES[range] || RANGES.all).label
  const anchor = rangeAnchor(data)

  /* hero */
  // 合计一格就够；pi / 外部两个绝对值放 sub，不做百分比占比 —— 合并序列里没有
  // 「pi 占多少」这个可成立的测量（rs.cost 已含外部，再除一次必然自指）。
  const splitSub = ext
    ? 'pi ' + money(piRs.cost) + ' + 外部 ' + money(re.totalCost)
    : 'models.dev 单价 · 摊算'
  const hero: Array<{ label: string; value: string; sub: string }> = [
    { label: rangeLabel + '费用', value: money(rs.cost), sub: splitSub },
    { label: rangeLabel + 'Token', value: fmt(rs.tokens), sub: '精确求和 · 含缓存读写' },
    { label: '日均费用', value: money(rs.avgCost), sub: '按 ' + rs.dayCount + ' 个活跃日' },
    { label: '单日最高', value: money(rs.maxCost), sub: rs.maxDay || '—' },
    { label: '单日最低', value: money(rs.minCost), sub: rs.minDay || '—' },
    { label: '缓存命中率', value: pct(rs.cacheHitPct), sub: '范围内缓存读占比' }
  ]
  /* 连续活跃按「全工具」合并序列算，锚点用页面的 rangeAnchor。
     旧稿取 data.days（只含 pi）并把锚点定成 pi 自己的最后一天，于是 pi 停在 09-20
     时读成「2 天 · 至 09-20」，而外部 CLI 一直用到 10-07 —— 真实连段是 30 天。
     同一行左边六格都是全工具口径，这一格不能例外。
     连段长度是绝对事实，不随范围切换（7 天窗口装不下 30 天的连段，硬裁只会
     永远显示满格），范围由 sub 里的终点日期自证。 */
  const stk = calcStreak(mergeDailyUsage(data), anchor)
  /* 「连续活跃」不再占一格 KPI。7 个格子在 1196px 的内容宽下按 180px 基准只排得下 6 个，
   * 落单那格会被 flex-grow 拉成整行宽的巨型空块。降级成走势行左侧的状态注记，
   * 顺手给原来只挂在 title 上的 sparkline 补一句文字标签（见 ui.css 的 .spark-row）。 */
  const streakCell = '<div class="kpi kpi-note"><div class="kpi-l">连续活跃</div>' +
    '<div class="kpi-v">' + (stk.streak > 0 ? stk.streak + ' 天' : '—') + '</div>' +
    '<div class="kpi-s">' + (stk.endDate ? '全工具 · 至 ' + esc(stk.endDate) : '全工具 · 断档或暂无数据') + '</div></div>'
  /* 1px 迷你 sparkline(ns-003):范围内逐日 token 归一走势 */
  const heroSpark = (function () {
    if (rs.days.length < 2) return ''
    const spW = 1000, spH = 26
    const spMax = Math.max.apply(null, rs.days.map(function (d: any) { return d.total })) || 1
    const spPts = rs.days.map(function (d: any, i: number) {
      return (i / (rs.days.length - 1) * spW).toFixed(1) + ',' +
        (spH - 3 - (d.total / spMax) * (spH - 6)).toFixed(1)
    }).join(' ')
    return '<span class="spark-cap">逐日 token</span>' +
      '<div class="spark-wrap" title="' + esc(rangeLabel + '逐日 token 迷你走势') + '">' +
      '<svg class="spark" viewBox="0 0 ' + spW + ' ' + spH + '" preserveAspectRatio="none" aria-hidden="true">' +
      '<polyline points="' + spPts + '"/></svg></div>'
  })()
  html.push('<section class="kpis">' + hero.map(function (k) {
    return '<div class="kpi"><div class="kpi-l">' + esc(k.label) +
      '</div><div class="kpi-v">' + esc(k.value) + '</div><div class="kpi-s">' + esc(k.sub) + '</div></div>'
  }).join('') + '<div class="spark-row">' + streakCell + heroSpark + '</div></section>')

  /* 窗口用量：各家额度存在服务端，本机读不到，所以只报实测用量，不给百分比。
     旧稿这块叫「限额」，让用户先填一个上限再拿实测 token 去除 —— 分母是随手编的
     数字，612.35M tok 除以它得到的百分比，精度全看填得准不准，界面却写得像在
     报告事实。默认状态下它还恒显三行「额度上限未设置」。

     版式上踩过三个坑，一并记在这里：
       - 每行重复「近 7 天 / 近 30 天」标签且不对齐，只能横着读；改成表头 + 网格。
       - 某个窗口无用量时整格省略，换行后下一格的数贴到了上一个工具身上；缺值写
         「无用量」，位置留着。
       - 两个窗口是嵌套的（近 30 天含近 7 天），并排列出会诱导读者相减；注释点破。
     金额那列的单位价跨工具差 40 倍以上（模型构成不同），不能当花费排序用，
     所以表头写明「API 等价 $」而不是「费用」。 */
  {
    const usage = windowUsage(data, st.policy)
    if (usage.length) {
      const shown = usage.slice(0, 5)
      const rest = usage.length - shown.length
      const num = function (s: string, cls: string) { return '<div class="' + cls + '">' + s + '</div>' }
      html.push('<div class="note win-note"><b>窗口用量</b> ' +
        '<span class="dim small">锚定最新数据日，不随上方范围切换</span>' +
        '<div class="win-grid">' +
        '<div class="win-h win-h-name">工具</div>' +
        '<div class="win-h win-h-n">近 7 天</div>' +
        '<div class="win-h win-h-n">近 30 天</div>' +
        '<div class="win-h win-h-n">近 7 天 API 等价 $</div>' +
        shown.map(function (u: WindowUsage) {
          const w7 = u.byWindow['7d']
          const w30 = u.byWindow['30d']
          const none = '无用量'
          return num(esc(u.label), 'win-name') +
            num(w7 && w7.tokens > 0 ? fmt(w7.tokens) : none, w7 && w7.tokens > 0 ? 'win-n' : 'win-none') +
            num(w30 && w30.tokens > 0 ? fmt(w30.tokens) : none, w30 && w30.tokens > 0 ? 'win-n' : 'win-none') +
            num(w7 && w7.tokens > 0 ? money(w7.cost) : none, w7 && w7.tokens > 0 ? 'win-n' : 'win-none')
        }).join('') + '</div>' +
        (rest > 0 ? '<div class="dim small">…另有 ' + rest + ' 个工具近 30 天有用量，见跨工具总览</div>' : '') +
        '<div class="note-inline dim small">这是从本机用量日志实测的用量，不是各家订阅额度 —— 额度在服务端，本机读不到。' +
        '两个窗口是嵌套的：近 30 天含近 7 天，不是两段不相交的时间。' +
        '金额是按 models.dev 均价折算的 API 等价成本，订阅制下你实际付的不是这个数；' +
        '各工具的综合单价因模型构成不同能差几十倍，这一列不能用来排谁更费钱。' +
        '本机工具的花费提醒在<a href="#/settings?range=all">设置页</a>的月预算。</div></div>')
    }
  }

  /* 趋势(范围过滤) */
  html.push('<section class="card">')
  html.push('<h2>' + esc(rangeLabel) + ' Token 与费用 <span class="hint">柱 = token（按 4 类拆分）；折线 = 当日估算费用（按各模型均价摊算）</span></h2>')
  html.push(dayChart(rs.days, costByDay))
  html.push(legend([{ label: '缓存读', color: CATS[0].color }, { label: '缓存写', color: CATS[1].color },
    { label: '新增输入', color: CATS[2].color }, { label: '输出', color: CATS[3].color },
    { label: '费用折线', color: 'var(--cost-line)' }]))
  html.push('</section>')

  /* 洞察行(缓存效率 / 最贵日+主模型 / 日均 burn / 月末预测) */
  const ins = rangeInsights(data, s, costByDay, range)
  const insItems: Array<{ label: string; value: string; sub: string }> = [
    { label: '缓存效率', value: pct(ins.cacheHitPct), sub: '读 ' + fmt(ins.cacheRead) + ' / (读+写+新增输入)' },
    {
      label: '最贵一天', value: money(ins.peakDayCost),
      sub: (ins.peakDay || '—') + (ins.peakModel ? ' · 主模型 ' + ins.peakModel : '')
    },
    { label: '日均 burn', value: money(ins.avgBurn), sub: '按 ' + ins.dayCount + ' 个活跃日' },
    { label: '月末预测', value: money(ins.forecast30), sub: '日均 × 30（示意，非结算）' }
  ]
    /* Optimize · 浪费提示 */
  {
    const tips: string[] = []
    const hit = rs.cacheHitPct || 0
    if (rs.tokens > 0 && hit < 20) {
      tips.push('缓存命中仅 ' + pct(hit) + '：重复长上下文较多，可减少新开会话或压缩 system/工具说明。')
    }
    if (rs.maxCost > 0 && rs.avgCost > 0 && rs.maxCost > rs.avgCost * 3) {
      tips.push('单日峰值 ' + money(rs.maxCost) + ' 约为日均 ' + money(rs.avgCost) + ' 的 ' + (rs.maxCost / rs.avgCost).toFixed(1) + ' 倍，检查是否有空转或批量任务。')
    }
    const unpriced = (s as any).unpriced || []
    if (unpriced.length) {
      tips.push(unpriced.length + ' 个模型未匹配单价，涉及 ' + fmt((s as any).unpricedTokens || 0) + ' token 未计入金额。')
    }
    if (tips.length) {
      html.push('<section class="card"><h2>优化提示 <span class="hint">Optimize · 确定性启发式</span></h2><ul class="tips">')
      tips.forEach(function (t) { html.push('<li>' + esc(t) + '</li>') })
      html.push('</ul></section>')
    }
  }
html.push('<section class="card insights">')
  html.push('<h2>洞察 <span class="hint">' + esc(rangeLabel) + ' · 随口径与范围实时变</span></h2>')
  html.push('<div class="ins-row">' + insItems.map(function (k) {
    return '<div class="ins"><div class="ins-l">' + esc(k.label) +
      '</div><div class="ins-v">' + esc(k.value) + '</div><div class="ins-s">' + esc(k.sub) + '</div></div>'
  }).join('') + '</div>')
  html.push('</section>')

  /* 本周 Top 模型(U6)。
     锚点用 pi 侧最后一天：dayModel 只有 pi 有，外部工具只有逐日合计与逐模型合计，
     没有逐日逐模型，用全工具锚点会算出一个空周。表头与聚合共用 isoWeekRange 且共用
     同一个 weekAnchor —— 旧稿表头按页面 anchor（10-07）算、数据按 piLast（09-20）算，
     于是表头写着 10-05~10-07，列的却是 09-15~09-20 的模型，声明了一个没有数据的周。 */
  const piLast = ((data.days || []) as any[]).map((d: any) => d.d).filter(Boolean).sort().pop() || anchor
  const weekAnchor = piLast
  const weekTop = weekTopModels(data, s, weekAnchor)
  const wk = isoWeekRange(weekAnchor)
  const weekStartNote = wk ? wk.start + ' ~ ' + wk.end : ''
  html.push('<section class="card">')
  html.push('<h2>本周 Top 模型 <span class="hint">' +
    esc(weekStartNote || '以数据最大日所在周计') +
    ' · 仅 pi 侧 dayModel · 按费用降序 · token 精确聚合</span></h2>')
  if (!weekTop.length) {
    html.push('<div class="empty">本周暂无数据 —— dayModel 缺失或该周无用量。</div>')
  } else {
    const colorIdxMap: Record<string, number> = {}
    ;(data.models || []).map(function (m: any) { return m.key }).sort().forEach(function (k: string, i: number) {
      colorIdxMap[k] = i
    })
    const tot = weekTop.reduce(function (t: number, r) { return t + r.cost }, 0) || 1
    html.push('<div class="week-top">')
    weekTop.forEach(function (r) {
      const color = MODEL_COLORS[(colorIdxMap[r.key] || 0) % MODEL_COLORS.length]
      html.push('<div class="week-row">' +
        '<i class="week-dot" style="background:' + color + '"></i>' +
        '<span class="week-name">' + esc(r.key) + '</span>' +
        '<span class="week-bar"><b style="width:' + pct(r.share) + ';background:' + color + '"></b></span>' +
        '<span class="week-tok">' + fmt(r.token) + '</span>' +
        '<span class="week-cost">' + money(r.cost) + '</span>' +
        '<span class="week-pct">' + pct(r.cost / tot * 100) + '</span>' +
        '</div>')
    })
    html.push('</div>')
    html.push('<div class="note-inline dim small">费用 = 周内 dayModel token × 该模型全量综合均价；占比按 Top 行内费用归一。</div>')
  }
  html.push('</section>')

  /* 日历 heatmap:weekday × hour,消费 hours 契约;缺 hours 时降级文案 */
  const rangeHours = filterHoursByRange((data.hours || []) as any, range, rangeAnchor(data))
  html.push('<section class="card">')
  html.push('<h2>活动热力图 <span class="hint">星期 × 小时 · 格色 = token 强度 · 鼠标悬停看该格明细</span></h2>')
  if (!(data.hours || []).length) {
    html.push('<div class="empty">重新跑 tokanary refresh 获取小时粒度（仓库需含 hours 字段）。</div>')
  } else {
    html.push(heatmap(hourMatrix(rangeHours)))
  }
  html.push('</section>')

  /* 跨工具(范围化) */
  if (ext) {
    const cmpBars = [{
      label: 'pi', sub: fmt(rs.fields.total) + ' · ' + money(rs.cost),
      total: rs.fields.total, totalText: fmt(rs.fields.total) + ' · ' + money(rs.cost),
      segments: CATS.map(function (c) { return { v: (rs.fields as any)[c.k] || 0, color: c.color, name: c.label } })
    }].concat(re.rows.filter(function (r: any) {
      // 范围内一个 token 都没有的工具不占行：旧稿把它们列成「0 · $0」再画一根
      // 恒定的灰竖线，两行纯噪音。
      return r.tokens > 0
    }).map(function (r: any) {
      return {
        label: r.label, sub: fmt(r.tokens) + ' · ' + money(r.cost),
        total: r.tokens, totalText: fmt(r.tokens) + ' · ' + money(r.cost),
        segments: CATS.map(function (c) { return { v: (r.fields as any)[c.k] || 0, color: c.color, name: c.label } })
      }
    }))
    html.push('<section class="card">')
    html.push('<h2>跨工具总览 · ' + esc(rangeLabel) + ' <span class="hint">token 精确求和；他家费用按综合均价摊算</span></h2>')
    html.push(stackBar(cmpBars))
    html.push(legend(CATS.map(function (c) { return { label: c.label, color: c.color } })))
    html.push('</section>')
  }

  /* 口径提示(复用全量对比数) */
  html.push('<div class="note"><b>计费口径</b>：范围 token 精确求和，费用按各模型综合均价摊算（与日趋势折线同法），随当前口径实时变。' +
    '全量 models.dev 口径 <b>' + money(cmp.modelsdev) + '</b>。</div>')
  return html.join('')
}

