import {
  fmt, money, pct, esc, CATS, POLICIES, SOURCES,
  normalizeCost, costOfTokens, computeAll, compareSources,
  dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor,
  filterHoursByRange, hourMatrix, rangeInsights, RANGES,
  filterDaysByRange, rangeCutoffKey, calcStreak, weekTopModels,
  type PricingOpts, type PriceSource, type RangeKey
} from '../pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, heatmap, trimNum } from '../charts'
import { th, sourceTag, stableColorIndex, sesRecency, pad2, localDay, type UiState } from './shared'

/**
 * 范围内按 model key 聚合：pi 的 dayModel 精确 + external 各工具按
 * 「工具日合计 × 该模型占比」近似（external 无 per-model day 明细）。
 */
function rangeModelAgg(data: any, range: RangeKey): Record<string, { total: number; fields: any }> {
  const anchor = rangeAnchor(data)
  const fdays = filterDaysByRange(data.days || [], range, anchor)
  const allow: Record<string, true> = {}
  fdays.forEach(function (d) { allow[d.d] = true })
  const out: Record<string, { total: number; fields: any }> = {}
  ;(data.dayModel || []).forEach(function (dm: any) {
    if (!allow[dm.d]) return
    let e = out[dm.key]
    if (!e) {
      e = out[dm.key] = { total: 0, fields: { cacheRead: 0, cacheWrite: 0, input: 0, output: 0 } }
    }
    e.total += dm.total || 0
  })
  // fields 无 dayModel 拆分:按全量模型比例近似回填(只用于堆叠条;token 总数仍精确)
  ;(data.models || []).forEach(function (m: any) {
    const e = out[m.key]
    if (!e || !m.total) return
    const scale = e.total / m.total
    e.fields.cacheRead = Math.round((m.cacheRead || 0) * scale)
    e.fields.cacheWrite = Math.round((m.cacheWrite || 0) * scale)
    e.fields.input = Math.round((m.input || 0) * scale)
    e.fields.output = Math.round((m.output || 0) * scale)
  })
  // external: 无 per-model 日拆，按工具范围 token × 模型占比近似
  for (const t of data?.external?.tools || []) {
    const tdays = filterDaysByRange(t.days || [], range, anchor)
    let rangeTok = 0
    for (const d of tdays) {
      rangeTok += Number(d.total || 0) ||
        Number(d.input || 0) + Number(d.output || 0) + Number(d.cacheRead || 0) + Number(d.cacheWrite || 0)
    }
    if (rangeTok <= 0) continue
    const models = t.models || []
    let allTok = 0
    for (const m of models) allTok += Number(m.total || 0)
    if (allTok <= 0) continue
    for (const m of models) {
      const share = Number(m.total || 0) / allTok
      const tok = Math.round(rangeTok * share)
      if (tok <= 0) continue
      let e = out[m.id]
      if (!e) {
        e = out[m.id] = { total: 0, fields: { cacheRead: 0, cacheWrite: 0, input: 0, output: 0 } }
      }
      const scale = Number(m.total || 0) ? share : 1
      e.total += tok
      e.fields.cacheRead += Math.round(Number(m.cacheRead || 0) * scale * (rangeTok / allTok))
      e.fields.cacheWrite += Math.round(Number(m.cacheWrite || 0) * scale * (rangeTok / allTok))
      e.fields.input += Math.round(Number(m.input || 0) * scale * (rangeTok / allTok))
      e.fields.output += Math.round(Number(m.output || 0) * scale * (rangeTok / allTok))
    }
  }
  return out
}

/** Tab 模型:范围内堆叠条 + 稳定色 donut + 明细表(改价/排序)。 */
export function renderModels(data: any, st: UiState, cmp: Record<string, number>, range: RangeKey): string {
  if (!data) {
    return '<div class="empty">没有用量数据 —— 请先运行 <code>tokanary refresh</code> 重建仓库。</div>'
  }
  const s = computeAll(data, st)
  const costByDay = dailyCost(data, s)
  const rs = rangeStats(data, s, costByDay, range)
  const rangeLabel = (RANGES[range] || RANGES.all).label
  const agg = rangeModelAgg(data, range)

  // 合并 pi + external 模型宇宙（external 可能有 pi 没有的 key）
  type MRow = { key: string; total: number; turns: number; cacheRead: number; cacheWrite: number; input: number; output: number; unit: number; source: string }
  const piRows: MRow[] = s.rows.map(function (r: any) {
    return {
      key: r.m.key, total: r.m.total, turns: r.m.turns,
      cacheRead: r.m.cacheRead, cacheWrite: r.m.cacheWrite, input: r.m.input, output: r.m.output,
      unit: r.unit || 0, source: r.price && r.price.source || 'missing'
    }
  })
  const piKeySet = new Set(piRows.map(function (r) { return r.key }))
  const extMeta: Record<string, { unit: number; source: string }> = {}
  for (const t of data?.external?.tools || []) {
    const prices = (data.external && data.external.prices) || {}
    for (const m of t.models || []) {
      const key = String(m.id)
      const pr = prices[key] || {}
      const cost = pr.cost || {}
      const unit = ((cost.input || 0) + (cost.output || 0) + (cost.cache_read || 0) + (cost.cache_write || 0)) / 4
      extMeta[key] = { unit: unit || 0, source: pr.source || 'missing' }
      if (!piKeySet.has(key)) {
        piRows.push({
          key, total: Number(m.total || 0), turns: 0,
          cacheRead: Number(m.cacheRead || 0), cacheWrite: Number(m.cacheWrite || 0),
          input: Number(m.input || 0), output: Number(m.output || 0),
          unit: unit || 0, source: pr.source || 'missing'
        })
      }
    }
  }

  const colorIdx = stableColorIndex(piRows.map(function (m) { return m.key }))
  const colorOf = function (key: string) {
    return MODEL_COLORS[(colorIdx[key] || 0) % MODEL_COLORS.length]
  }

  /* 堆叠条:按范围内 token 降序 */
  const stackRows = piRows.map(function (m: MRow) {
    const a = agg[m.key]
    const total = a ? a.total : 0
    const f = (a && a.fields) || { cacheRead: 0, cacheWrite: 0, input: 0, output: 0 }
    return {
      label: m.key,
      sub: (a ? '范围内 ' + fmt(total) : '范围内 0') + ' · 全量 ' + fmt(m.total),
      total, totalText: fmt(total),
      segments: CATS.map(function (c) { return { v: (f as any)[c.k] || 0, color: c.color, name: c.label } })
    }
  }).filter(function (x: any) { return x.total > 0 })
  stackRows.sort(function (a: any, b: any) { return b.total - a.total })

  /* donut:范围内 token × 综合均价 */
  const costItems = piRows.map(function (m: MRow) {
    const a = agg[m.key]
    const tokens = a ? a.total : 0
    const cost = m.unit * tokens / 1e6
    return { label: m.key, value: cost, color: colorOf(m.key) }
  }).filter(function (x: any) { return x.value > 0 })
  costItems.sort(function (a: any, b: any) { return b.value - a.value })

  const html: string[] = []
  html.push('<section class="kpis">')
  const mHero = [
    { label: rangeLabel + '模型数', value: String(stackRows.length || 0), sub: '有范围内用量的模型' },
    { label: rangeLabel + '模型费用', value: money(costItems.reduce(function (t: number, x: any) { return t + x.value }, 0)), sub: '按各模型均价摊算' },
    { label: '全量 Token', value: fmt(s.totalTokens), sub: '含缓存读写' },
    { label: '已匹配价格', value: s.rows.filter(function (r: any) { return r.price.source !== 'missing' }).length + '/' + s.rows.length, sub: '手动 > 自定义 > 口径' }
  ]
  html.push(mHero.map(function (k) {
    return '<div class="kpi"><div class="kpi-l">' + esc(k.label) +
      '</div><div class="kpi-v">' + esc(k.value) + '</div><div class="kpi-s">' + esc(k.sub) + '</div></div>'
  }).join(''))
  html.push('</section>')

  /* 堆叠条 */
  html.push('<section class="card">')
  html.push('<h2>' + esc(rangeLabel) + ' 模型 Token 构成 <span class="hint">稳定色 · 堆叠 = 缓存读/写/输入/输出</span></h2>')
  if (stackRows.length) {
    html.push(stackBar(stackRows, { labelW: 170 }))
    html.push(legend(CATS.map(function (c) { return { label: c.label, color: c.color } })))
  } else {
    html.push('<div class="empty">当前范围内没有模型用量 —— 换更大时间范围试试。</div>')
  }
  html.push('</section>')

  /* donut + 图例 */
  html.push('<section class="card">')
  html.push('<h2>' + esc(rangeLabel) + ' 模型费用占比 <span class="hint">色按模型 key 字典序固定,跨视图一致</span></h2>')
  html.push('<div class="cost-row">')
  html.push('<div class="donut-wrap">' + donut(costItems) + '</div>')
  html.push('<div class="cost-list">' + (costItems.length ? costItems.map(function (c: any) {
    const tot = costItems.reduce(function (t: number, x: any) { return t + x.value }, 0) || 1
    const share = c.value / tot * 100
    return '<div class="cost-li"><i style="background:' + c.color + '"></i><span class="cl-name">' + esc(c.label) +
      '</span><span class="cl-bar"><b style="width:' + pct(share) + ';background:' + c.color + '"></b></span>' +
      '<span class="cl-val">' + money(c.value) + '</span><span class="cl-pct">' + pct(share) + '</span>' +
      '<span class="cl-unit"></span></div>'
  }).join('') : '<div class="empty">尚无范围内费用</div>') + '</div>')
  html.push('</div>')
  html.push('</section>')

  /* 明细表(全量,与旧区块 6 同构;范围内 token 一列) */
  const sortKey = st.sortKey, dir = st.sortDir
  const tableRows = s.rows.slice().sort(function (a: any, b: any) {
    let va: any, vb: any
    if (sortKey === 'key') { va = a.key; vb = b.key; return va < vb ? -dir : va > vb ? dir : 0 }
    if (sortKey === 'cost') { va = a.cost; vb = b.cost }
    else if (sortKey === 'turns') { va = a.turns; vb = b.turns }
    else if (sortKey === 'rTotal') {
      va = (agg[a.m.key] || {}).total || 0
      vb = (agg[b.m.key] || {}).total || 0
    } else { va = (a.m as any)[sortKey] || 0; vb = (b.m as any)[sortKey] || 0 }
    return (va - vb) * dir
  })

  html.push('<section class="card">')
  html.push('<h2>模型明细与单价 <span class="hint">单价可直接改（localStorage）· 范围内 token 列随全局范围</span></h2>')
  html.push('<div class="tbl-wrap"><table class="tbl"><thead><tr>' +
    th(sortKey, dir, 'key', '模型（归一化）') + th(sortKey, dir, 'turns', '轮次') +
    th(sortKey, dir, 'rTotal', '范围内 Token') +
    th(sortKey, dir, 'cacheRead', '缓存读') + th(sortKey, dir, 'cacheWrite', '缓存写') + th(sortKey, dir, 'input', '新增输入') + th(sortKey, dir, 'output', '输出') +
    th(sortKey, dir, 'total', '合计 Token') + '<th>占比</th>' +
    '<th>输入 $/M</th><th>输出 $/M</th><th>缓存读 $/M</th><th>缓存写 $/M</th>' +
    th(sortKey, dir, 'cost', '费用') + '<th>费用占比</th><th>价格来源</th></tr></thead><tbody>')

  tableRows.forEach(function (r: any) {
    const m = r.m, p = r.price, v = p.values || {}
    const miss = p.source === 'missing'
    const rTot = (agg[m.key] || {}).total || 0
    html.push('<tr class="' + (miss ? 'row-miss' : '') + '">')
    html.push('<td class="mname"><b>' + esc(m.key) + '</b><div class="raw">' + esc(m.rawIds.join(' · ')) + '</div></td>')
    html.push('<td class="num">' + m.turns + (m.missingUsage ? ' <span class="warn" title="无 usage 记录">-' + m.missingUsage + '</span>' : '') + '</td>')
    html.push('<td class="num strong">' + fmt(rTot) + '</td>')
    html.push('<td class="num">' + fmt(m.cacheRead) + '</td>')
    html.push('<td class="num">' + fmt(m.cacheWrite) + '</td>')
    html.push('<td class="num">' + fmt(m.input) + '</td>')
    html.push('<td class="num">' + fmt(m.output) + '</td>')
    html.push('<td class="num strong">' + fmt(m.total) + '</td>')
    html.push('<td class="num">' + pct(r.share) + '</td>')
    ;['input', 'output', 'cache_read', 'cache_write'].forEach(function (f) {
      html.push('<td class="price"><input type="number" step="0.001" min="0" data-key="' + esc(m.key) +
        '" data-field="' + f + '" value="' + (v[f] === null || v[f] === undefined ? '' : trimNum(v[f])) +
        '" placeholder="—"></td>')
    })
    html.push('<td class="num strong">' + (r.priced ? money(r.cost) : '—') + '</td>')
    html.push('<td class="num">' + (r.priced ? pct(r.costShare) : '—') + '</td>')
    html.push('<td>' + sourceTag(m.key, p, st) + '</td>')
    html.push('</tr>')
  })
  html.push('</tbody></table></div>')
  if (s.unpriced.length) {
    html.push('<div class="warn-box">⚠️ 有 ' + s.unpriced.length + ' 个模型没有匹配到价格，涉及 ' +
      fmt(s.unpricedTokens) + ' token：' +
      s.unpriced.map(function (r: any) { return '<code>' + esc(r.m.key) + '</code>' }).join('、') +
      '。请在上表对应行填入单价。</div>')
  }
  html.push('</section>')

  html.push('<div class="note"><b>范围内口径</b>：token 取 <code>dayModel</code> 在 ' + esc(rangeLabel) +
    ' 的精确聚合；费用 = 范围内 token × 该模型全量综合均价（与总览日趋势折线同法）。' +
    '全量 models.dev <b>' + money(cmp.modelsdev) + '</b>。</div>')
  return html.join('')
}

