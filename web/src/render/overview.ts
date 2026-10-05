import {
  fmt, money, pct, esc, CATS, POLICIES, SOURCES,
  normalizeCost, costOfTokens, computeAll, compareSources,
  dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor, withMergedDays, mergeDailyCost,
  filterHoursByRange, hourMatrix, rangeInsights, RANGES,
  filterDaysByRange, rangeCutoffKey, calcStreak, weekTopModels,
  type PricingOpts, type PriceSource, type RangeKey
} from '../pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, heatmap, trimNum } from '../charts'
import { th, sourceTag, stableColorIndex, sesRecency, pad2, localDay, type UiState } from './shared'

export function renderOverview(data: any, st: UiState, cmp: Record<string, number>, range: RangeKey): string {
  if (!data) {
    return '<div class="empty">没有用量数据 —— 请先运行 <code>tokanary refresh</code> 重建仓库。</div>'
  }
  const s = computeAll(data, st)
  const ext = externalSummary(data, st.policy)
  const merged = withMergedDays(data)
  const costByDay = mergeDailyCost(dailyCost(data, s), ext, data)
  const rs = rangeStats(merged, s, costByDay, range)
  const re = rangeExt(ext, range, rangeAnchor(data))
  const html: string[] = []
  const rangeLabel = (RANGES[range] || RANGES.all).label
  const anchor = rangeAnchor(data)

  /* hero */
  const hero: Array<{ label: string; value: string; sub: string }> = [
    { label: rangeLabel + '费用', value: money(rs.cost), sub: 'models.dev 单价 · 摊算' },
    { label: rangeLabel + 'Token', value: fmt(rs.tokens), sub: '精确求和 · 含缓存读写' },
    { label: '日均费用', value: money(rs.avgCost), sub: '按 ' + rs.dayCount + ' 个活跃日' },
    { label: '单日最高', value: money(rs.maxCost), sub: rs.maxDay || '—' },
    { label: '单日最低', value: money(rs.minCost), sub: rs.minDay || '—' },
    { label: '缓存命中率', value: pct(rs.cacheHitPct), sub: '范围内缓存读占比' }
  ]
  // streak / week-top: pi day series only — use that series last day
  const piLast = ((data.days || []) as any[]).map((d: any) => d.d).filter(Boolean).sort().pop() || anchor
  const stk = calcStreak(data.days || [], piLast)
  hero.push({
    label: '连续活跃', value: stk.streak > 0 ? stk.streak + ' 天' : '—',
    sub: stk.endDate ? '至 ' + stk.endDate : '断档或暂无数据'
  })
  if (ext) {
    hero.push({
      label: rangeLabel + '全部工具', value: money(rs.cost + re.totalCost),
      sub: 'pi 占 ' + pct(rs.cost / ((rs.cost + re.totalCost) || 1) * 100)
    })
  }
  /* 1px 迷你 sparkline(ns-003):范围内逐日 token 归一走势 */
  const heroSpark = (function () {
    if (rs.days.length < 2) return ''
    const spW = 1000, spH = 26
    const spMax = Math.max.apply(null, rs.days.map(function (d: any) { return d.total })) || 1
    const spPts = rs.days.map(function (d: any, i: number) {
      return (i / (rs.days.length - 1) * spW).toFixed(1) + ',' +
        (spH - 3 - (d.total / spMax) * (spH - 6)).toFixed(1)
    }).join(' ')
    return '<div class="spark-row" title="' + esc(rangeLabel + '逐日 token 迷你走势') + '">' +
      '<svg class="spark" viewBox="0 0 ' + spW + ' ' + spH + '" preserveAspectRatio="none" aria-hidden="true">' +
      '<polyline points="' + spPts + '"/></svg></div>'
  })()
  html.push('<section class="kpis">' + hero.map(function (k) {
    return '<div class="kpi"><div class="kpi-l">' + esc(k.label) +
      '</div><div class="kpi-v">' + esc(k.value) + '</div><div class="kpi-s">' + esc(k.sub) + '</div></div>'
  }).join('') + heroSpark + '</section>')

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
  html.push('<section class="card insights">')
  html.push('<h2>洞察 <span class="hint">' + esc(rangeLabel) + ' · 随口径与范围实时变</span></h2>')
  html.push('<div class="ins-row">' + insItems.map(function (k) {
    return '<div class="ins"><div class="ins-l">' + esc(k.label) +
      '</div><div class="ins-v">' + esc(k.value) + '</div><div class="ins-s">' + esc(k.sub) + '</div></div>'
  }).join('') + '</div>')
  html.push('</section>')

  /* 本周 Top 模型(U6):数据最大日所在 ISO 周(周一起) */
  const weekTop = weekTopModels(data, s, piLast)
  const weekStartNote = (function () {
    if (!anchor) return ''
    const t = new Date(anchor + 'T00:00:00').getTime()
    if (isNaN(t)) return ''
    const back = (new Date(t).getDay() + 6) % 7
    const ws = new Date(t - back * 86400000)
    const pad = function (x: number) { return String(x).padStart(2, '0') }
    return ws.getFullYear() + '-' + pad(ws.getMonth() + 1) + '-' + pad(ws.getDate()) + ' ~ ' + anchor
  })()
  html.push('<section class="card">')
  html.push('<h2>本周 Top 模型 <span class="hint">' +
    esc(weekStartNote || '以数据最大日所在周计') + ' · 按费用降序 · token 精确聚合</span></h2>')
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
    }].concat(re.rows.map(function (r) {
      return {
        label: r.label, sub: fmt(r.tokens) + ' · ' + money(r.cost),
        total: r.tokens, totalText: fmt(r.tokens) + ' · ' + money(r.cost),
        segments: CATS.map(function (c) { return { v: (r.fields as any)[c.k] || 0, color: c.color, name: c.label } })
      }
    }))
    html.push('<section class="card">')
    html.push('<h2>跨工具总览 · ' + esc(rangeLabel) + ' <span class="hint">token 精确求和；他家费用按综合均价摊算</span></h2>')
    html.push(stackBar(cmpBars, { labelW: 132 }))
    html.push(legend(CATS.map(function (c) { return { label: c.label, color: c.color } })))
    html.push('</section>')
  }

  /* 口径提示(复用全量对比数) */
  html.push('<div class="note"><b>计费口径</b>：范围 token 精确求和，费用按各模型综合均价摊算（与日趋势折线同法），随当前口径实时变。' +
    '全量 models.dev 口径 <b>' + money(cmp.modelsdev) + '</b>。</div>')
  return html.join('')
}

