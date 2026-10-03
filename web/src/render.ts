/* 整页 HTML 构造器 —— 分区块模块 + 页面装配。 */
import {
  fmt, money, pct, esc, CATS, POLICIES, SOURCES,
  normalizeCost, costOfTokens, computeAll, compareSources,
  dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor,
  filterHoursByRange, hourMatrix, rangeInsights, RANGES,
  filterDaysByRange, rangeCutoffKey, calcStreak, weekTopModels,
  type PricingOpts, type PriceSource, type RangeKey
} from './pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, heatmap, trimNum } from './charts'
import { defaultUiState, th, sourceTag, type UiState } from './render/shared'
import { renderOverview } from './render/overview'
import { renderModels } from './render/models'
import { renderSessions } from './render/sessions'
import { budgetAlertHtml, renderProjects } from './render/projects'
import { renderSettings } from './render/settings'

export type { UiState } from './render/shared'
export { defaultUiState } from './render/shared'
export { renderOverview } from './render/overview'
export { renderModels } from './render/models'
export { renderSessions } from './render/sessions'
export { budgetAlertHtml, renderProjects } from './render/projects'
export { renderSettings } from './render/settings'

export function renderPage(data: any, st: UiState, cmp: Record<string, number>, ioText: string): string {
  if (!data) {
    return '<div class="empty">没有用量数据 —— 请先运行 <code>tokanary refresh</code> 重建仓库。</div>'
  }
  const s = computeAll(data, st)
  const costByDay = dailyCost(data, s)
  const meta = data.meta || {}
  const ext = externalSummary(data, st.policy)

  /* --- KPI --- */
  const days = (data.days || []).filter(function (d: any) { return d.total > 0 })
  const dayCount = days.length || 1
  const allTokens = s.totalTokens + (ext ? ext.totalTokens : 0)
  const allCost = s.totalCost + (ext ? ext.totalCost : 0)
  const kpis: Array<{ label: string; value: string; sub: string }> = [
    { label: 'Token 总量', value: fmt(s.totalTokens), sub: 'pi · 含缓存读写' },
    { label: '估算费用', value: money(s.totalCost), sub: s.coverage < 99.99 ? '已覆盖 ' + pct(s.coverage) + ' 的 token' : 'models.dev 单价' },
    { label: '缓存命中率', value: pct((data.totals || {}).cacheHitPct || 0), sub: '缓存读 / (读+写+新增输入)' },
    { label: '模型调用', value: String((data.totals || {}).turns || 0), sub: (data.totals || {}).turnsWithUsage + ' 轮有用量记录' },
    { label: '会话 / 项目', value: (data.totals || {}).sessions + ' / ' + (data.projects || []).length, sub: '覆盖 ' + dayCount + ' 个活跃日' },
    { label: '日均费用', value: money(s.totalCost / dayCount), sub: '按 ' + dayCount + ' 个活跃日摊算' }
  ]
  if (ext) {
    kpis.push({
      label: '全部工具 Token', value: fmt(allTokens),
      sub: 'pi + ' + ext.rows.map(function (r: any) { return r.t.label }).join(' + ')
    })
    kpis.push({
      label: '全部工具费用', value: money(allCost),
      sub: 'pi 占 ' + pct(s.totalCost / (allCost || 1) * 100) + ' · 其余 ' + pct(ext.totalCost / (allCost || 1) * 100)
    })
  }

  /* --- 图表 1：按模型 token 构成 --- */
  const bars = s.rows.filter(function (r: any) { return r.m.total > 0 }).map(function (r: any) {
    return {
      label: r.m.key, sub: r.m.turns + ' 轮 · ' + pct(r.share) + ' 占比',
      total: r.m.total, totalText: fmt(r.m.total),
      segments: CATS.map(function (c) { return { v: r.m[c.k] || 0, color: c.color, name: c.label } })
    }
  })

  /* --- 图表 2：成本环形 --- */
  const costItems = s.rows.filter(function (r: any) { return r.cost > 0 }).map(function (r: any, i: number) {
    return { label: r.m.key, value: r.cost, color: MODEL_COLORS[i % MODEL_COLORS.length] }
  })

  /* --- 表格排序 --- */
  const sortKey = st.sortKey, dir = st.sortDir
  const tableRows = s.rows.slice().sort(function (a: any, b: any) {
    let va: any, vb: any
    if (sortKey === 'key') { va = a.m.key; vb = b.m.key; return va < vb ? -dir : va > vb ? dir : 0 }
    if (sortKey === 'cost') { va = a.cost; vb = b.cost }
    else if (sortKey === 'turns') { va = a.m.turns; vb = b.m.turns }
    else { va = a.m[sortKey] || 0; vb = b.m[sortKey] || 0 }
    return (va - vb) * dir
  })

  const html: string[] = []

  html.push('<section class="kpis">' + kpis.map(function (k) {
    return '<div class="kpi"><div class="kpi-l">' + esc(k.label) +
      '</div><div class="kpi-v">' + esc(k.value) + '</div><div class="kpi-s">' + esc(k.sub) + '</div></div>'
  }).join('') + '</section>')

  /* 口径提示 */
  const gwMeta = {} as any
  html.push('<div class="note">' +
    '<b>计费口径</b>：金额 = Σ(缓存读 × 缓存读单价 + 缓存写 × 缓存写单价 + 新增输入 × 输入单价 + 输出 × 输出单价)，' +
    '单价单位为 USD / 每 100 万 token。' +
    'pi 的 <code>turns.input_tokens</code> 只含新增输入（不含缓存读写），因此本页一律以 <code>usage_json</code> 为准。<br>' +
    '<b>价格来源</b>：<b>models.dev 公开价</b>' +
    '（本机 ' + s.rows.length + ' 个模型，单价来自 models.dev。' +
    '按 <b>models.dev</b> 合计 <b>' + money(cmp.modelsdev) + '</b>' +
    '。<br>' +
    '缺 cache 单价时按 <b>' + esc(((POLICIES as any)[st.policy] || {}).label) + '</b> 推算，被推算的在表格里标' +
    '<span class="tag tag-est">推算</span>。' +
    '' +
    '</div>')

  /* 区块 1 */
  html.push('<section class="card">')
  html.push('<h2>按模型的 Token 构成 <span class="hint">堆叠条 = 该模型的 token 结构，长度按总量排序</span></h2>')
  html.push(stackBar(bars))
  html.push(legend(CATS.map(function (c) {
    const v = (data.totals || {})[c.k] || 0
    return { label: c.label, color: c.color, value: v, valueText: fmt(v) + '（' + pct(v / (s.totalTokens || 1) * 100) + '）' }
  })))
  html.push('</section>')

  /* 区块 2：成本 */
  html.push('<section class="card">')
  html.push('<h2>按模型的费用占比 <span class="hint">口径：上面各模型的 token × 当前所选单价</span></h2>')
  html.push('<div class="cost-row">')
  html.push('<div class="donut-wrap">' + donut(costItems) + '</div>')
  html.push('<div class="cost-list">' + (costItems.length ? costItems.map(function (c: any) {
    const r = s.rows.filter(function (x: any) { return x.m.key === c.label })[0]
    return '<div class="cost-li"><i style="background:' + c.color + '"></i><span class="cl-name">' + esc(c.label) +
      '</span><span class="cl-bar"><b style="width:' + pct(r.costShare) + ';background:' + c.color + '"></b></span>' +
      '<span class="cl-val">' + money(c.value) + '</span><span class="cl-pct">' + pct(r.costShare) + '</span>' +
      '<span class="cl-unit">' + (r.unit ? '$' + r.unit.toFixed(2) + '/M' : '') + '</span></div>'
  }).join('') : '<div class="empty">尚无价格数据</div>') + '</div>')
  html.push('</div>')
  html.push('</section>')

  /* 区块 3：趋势 */
  html.push('<section class="card">')
  html.push('<h2>每日 Token 与费用 <span class="hint">柱 = token（按 4 类拆分）；折线 = 当日估算费用（按各模型均价摊算）</span></h2>')
  html.push(dayChart(days, costByDay))
  html.push(legend([{ label: '缓存读', color: CATS[0].color }, { label: '缓存写', color: CATS[1].color },
    { label: '新增输入', color: CATS[2].color }, { label: '输出', color: CATS[3].color },
    { label: '费用折线', color: 'var(--cost-line)' }]))
  html.push('</section>')

  /* 区块 4：项目 */
  const projBars = (data.projects || []).filter(function (p: any) { return p.total > 0 }).map(function (p: any) {
    return {
      label: p.name, sub: p.sessions + ' 个会话 · ' + p.turns + ' 轮',
      total: p.total, totalText: fmt(p.total),
      segments: CATS.map(function (c) { return { v: p[c.k] || 0, color: c.color, name: c.label } })
    }
  })
  html.push('<section class="card">')
  html.push('<h2>按项目的 Token 分布 <span class="hint">来自 sessions.project_id</span></h2>')
  html.push(stackBar(projBars, { labelW: 150 }))
  html.push('</section>')

  /* 区块 5：其他 CLI（Claude Code / Codex / OpenCode） */
  if (ext) {
    const cmpBars = [{
      label: 'pi', sub: (data.totals || {}).sessions + ' 会话 · ' + (data.totals || {}).turns + ' 次调用',
      total: s.totalTokens, totalText: fmt(s.totalTokens) + ' · ' + money(s.totalCost),
      segments: CATS.map(function (c) { return { v: (data.totals || {})[c.k] || 0, color: c.color, name: c.label } })
    }].concat(ext.rows.map(function (r: any) {
      return {
        label: r.t.label, sub: r.t.sessions + ' 会话 · ' + r.t.calls + ' 次调用',
        total: r.t.total, totalText: fmt(r.t.total) + ' · ' + money(r.cost),
        segments: CATS.map(function (c) { return { v: r.t[c.k] || 0, color: c.color, name: c.label } })
      }
    }))

    html.push('<section class="card">')
    html.push('<h2>跨工具总览 <span class="hint">' +
      'pi 之外还会统计 Claude Code / Codex CLI / OpenCode / DeepSeek Harness（官方与三方包装壳分适配器）；三条口径已对齐：' +
      '<code>input</code>=非缓存输入、<code>output</code> 已含 reasoning（不重复计费）、成本只算 4 个计费分项' +
      '</span></h2>')
    html.push(stackBar(cmpBars, { labelW: 132 }))
    html.push(legend(CATS.map(function (c) {
      return { label: c.label, color: c.color }
    })))
    html.push('<div class="tool-cards">')
    ext.rows.forEach(function (r: any) {
      html.push('<div class="tool-card">')
      html.push('<div class="tc-head"><b>' + esc(r.t.label) + '</b><span class="tc-cost">' + money(r.cost) + '</span></div>')
      html.push('<div class="tc-row"><span>Token</span><b>' + fmt(r.t.total) + '</b></div>')
      html.push('<div class="tc-row"><span>会话 / 调用</span><b>' + r.t.sessions + ' / ' + r.t.calls + '</b></div>')
      html.push('<div class="tc-row"><span>缓存读</span><b>' + fmt(r.t.cacheRead) + '</b></div>')
      html.push('<div class="tc-row"><span>缓存写</span><b>' + fmt(r.t.cacheWrite) + '</b></div>')
      html.push('<div class="tc-row"><span>新增输入</span><b>' + fmt(r.t.input) + '</b></div>')
      html.push('<div class="tc-row"><span>输出</span><b>' + fmt(r.t.output) + '</b></div>')
      html.push('<div class="tc-row"><span>综合均价</span><b>' + (r.unit ? '$' + r.unit.toFixed(3) + '/M' : '—') + '</b></div>')
      html.push('<div class="tc-foot">' +
        (r.t.firstTs ? esc(r.t.firstTs.slice(0, 10)) + ' ~ ' + esc((r.t.lastTs || '').slice(0, 10)) : '') +
        '</div>')
      if (r.t.note) html.push('<div class="tc-note" title="' + esc(r.t.note) + '">' + esc(r.t.note.length > 96 ? r.t.note.slice(0, 96) + '…' : r.t.note) + '</div>')
      html.push('</div>')
    })
    html.push('</div>')
    if (ext.unpricedTokens > 0) {
      html.push('<div class="warn-box">⚠️ 其他 CLI 里有 ' + fmt(ext.unpricedTokens) +
        ' token 对应的模型没有单价，未计入金额（例如免费额度模型按 $0 计）。</div>')
    }
    html.push('</section>')

    /* 各工具的模型明细 */
    html.push('<section class="card">')
    html.push('<h2>其他 CLI 的模型明细 <span class="hint">单价来自 models.dev</span></h2>')
    html.push('<div class="tbl-wrap"><table class="tbl"><thead><tr>' +
      '<th>工具</th><th>模型</th><th class="num">缓存读</th><th class="num">缓存写</th>' +
      '<th class="num">新增输入</th><th class="num">输出</th><th class="num">合计</th>' +
      '<th class="num">均价 $/M</th><th>价格来源</th><th class="num">费用</th></tr></thead><tbody>')
    ext.rows.forEach(function (r: any) {
      r.models.forEach(function (m: any) {
        const src = m.price.source
        const tag = src === 'models.dev' ? '<span class="tag tag-ok">models.dev</span>'
          : src === 'models.dev' ? '<span class="tag tag-ok">models.dev</span>'
            : src === 'free' ? '<span class="tag tag-use">免费额度</span>'
              : '<span class="tag tag-miss">未收录</span>'
        html.push('<tr>')
        html.push('<td class="dim">' + esc(r.t.label) + '</td>')
        html.push('<td class="mname"><b>' + esc(m.id) + '</b>' +
          (m.price.matchedKey && m.price.matchedKey !== m.id ? '<div class="raw">→ ' + esc(m.price.matchedKey) + '</div>' : '') + '</td>')
        html.push('<td class="num">' + fmt(m.tok.cacheRead) + '</td>')
        html.push('<td class="num">' + fmt(m.tok.cacheWrite) + '</td>')
        html.push('<td class="num">' + fmt(m.tok.input) + '</td>')
        html.push('<td class="num">' + fmt(m.tok.output) + '</td>')
        html.push('<td class="num strong">' + fmt(m.tok.total) + '</td>')
        html.push('<td class="num">' + (m.unit ? '$' + m.unit.toFixed(3) : '—') + '</td>')
        html.push('<td>' + tag + (m.estimated ? ' <span class="tag tag-est" title="缺单价字段，按缺价策略推算">推算</span>' : '') + '</td>')
        html.push('<td class="num strong">' + (m.priced ? money(m.cost) : '—') + '</td>')
        html.push('</tr>')
      })
    })
    html.push('</tbody></table></div>')
    html.push('<div class="note-inline dim small">采集自 ' +
      ext.rows.map(function (r: any) { return '<code>' + esc(r.t.home || '') + '</code>'; }).join('、') +
      '，生成于 ' + esc(ext.generatedAt || '未知') + '。重跑 <code>tokanary refresh</code> 更新。</div>')
    html.push('</section>')
  }

  /* 区块 6：模型明细表 */
  html.push('<section class="card">')
  html.push('<h2>模型明细与单价 <span class="hint">单价可直接改，改动只存在本机浏览器（localStorage），实时重算</span></h2>')
  html.push('<div class="tbl-wrap"><table class="tbl"><thead><tr>' +
    th(sortKey, dir, 'key', '模型（归一化）') + th(sortKey, dir, 'turns', '轮次') +
    th(sortKey, dir, 'cacheRead', '缓存读') + th(sortKey, dir, 'cacheWrite', '缓存写') + th(sortKey, dir, 'input', '新增输入') + th(sortKey, dir, 'output', '输出') +
    th(sortKey, dir, 'total', '合计 Token') + '<th>占比</th>' +
    '<th>输入 $/M</th><th>输出 $/M</th><th>缓存读 $/M</th><th>缓存写 $/M</th>' +
    th(sortKey, dir, 'cost', '费用') + '<th>费用占比</th><th>价格来源</th></tr></thead><tbody>')

  tableRows.forEach(function (r: any) {
    const m = r.m, p = r.price, v = p.values || {}
    const miss = p.source === 'missing'
    html.push('<tr class="' + (miss ? 'row-miss' : '') + '">')
    html.push('<td class="mname"><b>' + esc(m.key) + '</b><div class="raw">' + esc(m.rawIds.join(' · ')) + '</div></td>')
    html.push('<td class="num">' + m.turns + (m.missingUsage ? ' <span class="warn" title="无 usage 记录">-' + m.missingUsage + '</span>' : '') + '</td>')
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
    html.push('<div class="warn-box">⚠️ 有 ' + s.unpriced.length + ' 个模型没有匹配到 models.dev 价格，涉及 ' +
      fmt(s.unpricedTokens) + ' token（占总量 ' + pct(100 - s.coverage) + '）：' +
      s.unpriced.map(function (r: any) { return '<code>' + esc(r.m.key) + '</code>' }).join('、') +
      '。请在上表对应行填入单价，费用会自动重算。</div>')
  }
  html.push('</section>')

  /* 价格设置 */
  const policyMatters = s.rows.some(function (r: any) {
    return r.m.cacheWrite > 0 && (r.price.estimatedFields || []).indexOf('cache_write') >= 0
  })
  const customCount = st.customPrices ? Object.keys(st.customPrices).length : 0
  html.push('<section class="card">')
  html.push('<h2>价格设置</h2>')
  html.push('<div class="settings">')
  html.push('<div class="set-row"><span>计费口径：</span>' +
    Object.keys(SOURCES).map(function (k) {
      return '<label class="radio"><input type="radio" name="psrc" value="' + k + '"' +
        (st.priceSource === k ? ' checked' : '') + '> ' + esc((SOURCES as any)[k].label) +
        ' <b>' + money((cmp as any)[k]) + '</b></label>'
    }).join('') +
    '<span class="dim small">切换后全页金额重算；每行仍可手动改价覆盖</span></div>')
  html.push('<label class="set-row"><span>缺失缓存单价时：</span><select id="policy">' +
    Object.keys(POLICIES).map(function (k) {
      return '<option value="' + k + '"' + (k === st.policy ? ' selected' : '') + '>' + esc((POLICIES as any)[k].label) + '</option>'
    }).join('') + '</select>' +
    (policyMatters ? '' :
      '<span class="dim small">当前数据里没有「有缓存写用量但缺单价」的模型，切换此项不影响金额（仅作为以后数据的兜底）</span>') +
    '</label>')
  html.push('<div class="set-row"><span>价格来源：</span><span class="src-info">' +
    '网关价目表 ' + esc((data.gateway || {}).source || '自有网关') +
    ' · 抓取于 ' + esc((data.gateway || {}).fetchedAt || '未知') +
    ' ｜ models.dev ' + esc((data.pricingMeta || {}).source || '') +
    ' · 抓取于 ' + esc((data.pricingMeta || {}).fetchedAt || '未知') +
    ' · 已匹配 ' + s.rows.filter(function (r: any) { return r.price.source !== 'missing' }).length + '/' + s.rows.length + ' 个模型</span></div>')
  html.push('<div class="set-row"><span>自定义价格源：</span>' +
    '<input id="custom-url" type="url" style="min-width:280px;flex:1" placeholder="https://…/prices.json（公开价格 JSON，无需 Key）" value="' + esc(st.customUrl) + '">' +
    '<button id="btn-custom-load">加载</button>' +
    '<button id="btn-custom-clear">清除</button></div>')
  html.push('<div class="dim small">' +
    (st.customError ? '⚠️ ' + esc(st.customError) :
      customCount
        ? '已加载自定义源 ' + customCount + ' 个模型' + (st.customFetchedAt ? ' · 抓取于 ' + esc(st.customFetchedAt) : '') + '（优先级：手动改价 > 自定义源 > 所选口径）'
        : '未启用。粘贴公开价格 JSON 的 URL 后点「加载」；需要鉴权的私有端点不支持填 Key，请走构建时脚本抓取后导入。') +
    '</div>')
  html.push('<div class="set-actions">' +
    '<button id="btn-reset">重置为 models.dev 价格</button>' +
    '<button id="btn-export">导出价格 JSON</button>' +
    '<button id="btn-import">导入价格 JSON</button>' +
    '<button id="btn-reload">重新读取</button>' +
    '</div>')
  html.push('<textarea id="io" placeholder="导出的 JSON 会出现在这里；粘贴 JSON 后点「导入价格 JSON」">' + esc(ioText) + '</textarea>')
  html.push('</div></section>')

  /* 区块 7：网关价目表 */
  const gw = data.gateway
  if (gw && gw.models && gw.models.length) {
    const rawUsageById: Record<string, any> = {}
    s.rows.forEach(function (r: any) {
      ;(r.m.rawUsage || []).forEach(function (ru: any) { rawUsageById[ru.id] = ru })
    })

    const q = st.gwQuery.trim().toLowerCase()
    const list = gw.models.filter(function (g: any) {
      return !q || g.id.toLowerCase().indexOf(q) >= 0
    })
    const gwSort = st.gwSort
    list.sort(function (a: any, b: any) {
      if (gwSort === 'inuse') {
        if (!!b.inUse !== !!a.inUse) return b.inUse ? 1 : -1
        return (b.total || 0) - (a.total || 0) || (b.input || 0) - (a.input || 0)
      }
      if (gwSort === 'id') return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
      if (gwSort === 'input') return (b.input || -1) - (a.input || -1)
      if (gwSort === 'output') return (b.output || -1) - (a.output || -1)
      if (gwSort === 'usage') {
        const ua = rawUsageById[a.id] ? rawUsageById[a.id].total : 0
        const ub = rawUsageById[b.id] ? rawUsageById[b.id].total : 0
        return ub - ua
      }
      return 0
    })

    const inUse = gw.models.filter(function (g: any) { return g.inUse }).length
    const pricedCount = gw.models.filter(function (g: any) { return g.priced }).length

    html.push('<section class="card">')
    html.push('<h2>网关价目表 · 自有网关 <span class="hint">' +
      '共 ' + gw.models.length + ' 个模型 · ' + pricedCount + ' 个有报价 · ' + inUse +
      ' 个本机用过；价格取自网关自己的 LiteLLM 成本表（<code>/public/litellm_model_cost_map</code>）</span></h2>')
    html.push('<div class="gw-bar">' +
      '<input id="gw-search" type="search" placeholder="搜索模型 id…" value="' + esc(st.gwQuery) + '">' +
      '<select id="gw-sort">' +
      ['inuse:本机用过的排前面', 'usage:按本机用量', 'input:按输入价', 'output:按输出价', 'id:按名称']
        .map(function (o) {
          const kv = o.split(':')
          return '<option value="' + kv[0] + '"' + (st.gwSort === kv[0] ? ' selected' : '') + '>' + kv[1] + '</option>'
        }).join('') + '</select>' +
      '<span class="dim small">显示 ' + list.length + ' / ' + gw.models.length + ' 个</span>' +
      '</div>')

    html.push('<div class="tbl-wrap"><table class="tbl gw-tbl"><thead><tr>' +
      '<th>模型 id</th><th>类型</th><th>上下文</th><th>最大输出</th>' +
      '<th class="num">输入 $/M</th><th class="num">输出 $/M</th>' +
      '<th class="num">缓存读 $/M</th><th class="num">缓存写 $/M</th>' +
      '<th>报价来源</th><th class="num">本机用量</th><th class="num">本机费用</th></tr></thead><tbody>')

    list.forEach(function (g: any) {
      const ru = rawUsageById[g.id]
      const gwPrice = { input: g.input, output: g.output, cache_read: g.cache_read, cache_write: g.cache_write }
      const eff = normalizeCost(gwPrice, st.policy)
      const myCost = (ru && eff.ok) ? costOfTokens(ru, eff.values) : 0
      const pnum = function (v: any) { return v === null || v === undefined ? '<span class="dim">—</span>' : '$' + trimNum(v) }
      html.push('<tr class="' + (g.inUse ? 'gw-inuse' : '') + '">')
      html.push('<td class="mname"><b>' + esc(g.id) + '</b>' +
        (g.inUse ? ' <span class="tag tag-use">本机用过</span>' : '') + '</td>')
      html.push('<td class="dim">' + esc(g.mode || 'chat') + '</td>')
      html.push('<td class="num dim">' + (g.max_input_tokens ? fmt(g.max_input_tokens) : '—') + '</td>')
      html.push('<td class="num dim">' + (g.max_output_tokens ? fmt(g.max_output_tokens) : '—') + '</td>')
      html.push('<td class="num">' + pnum(g.input) + '</td>')
      html.push('<td class="num">' + pnum(g.output) + '</td>')
      html.push('<td class="num">' + pnum(g.cache_read) + '</td>')
      html.push('<td class="num">' + pnum(g.cache_write) + '</td>')
      html.push('<td>' + (g.priced
        ? '<span class="tag tag-gw">成本表</span> <span class="dim small">' + esc(g.matchedKey || '') + '</span>'
        : '<span class="tag tag-miss">网关未收录</span>') + '</td>')
      html.push('<td class="num">' + (ru ? fmt(ru.total) : '<span class="dim">—</span>') + '</td>')
      html.push('<td class="num">' + (ru && eff.ok ? money(myCost) : '<span class="dim">—</span>') + '</td>')
      html.push('</tr>')
    })
    html.push('</tbody></table></div>')
    html.push('<div class="note-inline dim small">' +
      '「本机费用」= 该模型 id 在本机实际产生的 token × 网关单价（缓存写缺失时按上面的缺价策略推算）。' +
      '网关未收录的模型（Kimi 全系、豆包、部分图像模型）在账单里会回退到 models.dev 价格。' +
      '</div>')
    html.push('</section>')
  }

  /* 区块 8：会话 Top */
  html.push('<section class="card">')
  html.push('<h2>Top 会话 <span class="hint">按 token 排序，最多 ' + (data.sessions || []).length + ' 条</span></h2>')
  html.push('<div class="tbl-wrap"><table class="tbl"><thead><tr><th>#</th><th>标题</th><th>项目</th><th>模型</th>' +
    '<th>轮次</th><th>消息</th><th>合计 Token</th><th>输出</th><th>缓存读</th><th>更新时间</th></tr></thead><tbody>')
  ;(data.sessions || []).forEach(function (x: any, i: number) {
    html.push('<tr><td class="num">' + (i + 1) + '</td><td class="mname">' + esc(x.title) + '</td>' +
      '<td>' + esc(x.project) + '</td><td><code>' + esc(x.model) + '</code></td>' +
      '<td class="num">' + x.turns + '</td><td class="num">' + x.messages + '</td>' +
      '<td class="num strong">' + fmt(x.total) + '</td><td class="num">' + fmt(x.output) + '</td>' +
      '<td class="num">' + fmt(x.cacheRead) + '</td>' +
      '<td class="num dim">' + (x.updatedAt ? new Date(x.updatedAt).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '') + '</td></tr>')
  })
  html.push('</tbody></table></div>')
  html.push('</section>')

  /* 脚注 */
  html.push('<footer class="foot">数据源 <code>' + esc(meta.sourceNote || meta.piDir || meta.dbPath) + '</code>' +
    '（仓库 <code>' + esc(meta.dbPath) + '</code>' +
    (meta.dbSizeMB ? '，' + esc(meta.dbSizeMB) + ' MB' : '') +
    '）· 区间 ' + esc(meta.rangeStart) + ' ~ ' + esc(meta.rangeEnd) +
    ' · 生成于 ' + esc(meta.generatedAt) + '<br>金额为 models.dev 公开成本估算，非实际结算价；' +
    '缓存单价缺失时按所选策略推算，仅供参考。</footer>')

  return html.join('')
}

/** 与旧 rendertest 对齐的冒烟断言(供单测复用):返回缺失的区块名 */
export function missingSections(html: string, data?: any): string[] {
  // These must match the <h2> titles renderPage actually emits. They drifted
  // once already - the old list said 花费/账单 while the markup says 费用/单价 -
  // which silently turned this check into a no-op for five of the sections.
  const markers = ['按模型的 Token 构成', '按模型的费用占比', '每日 Token 与费用', '按项目的 Token 分布',
    '模型明细与单价', '价格来源', 'Top 会话', 'data-field="cache_read"', '<svg']
  // The per-model channel dropdown only exists when a model actually offers
  // more than one channel. With no price table (a fresh clone ships none -
  // .cache/prices-raw.json is generated locally) there is nothing to switch between, so
  // requiring the marker would report a section that was never applicable.
  // Pass `data` to make the check honest; omit it to demand the dropdown.
  if (!data || hasVariants(data)) markers.push('data-variant=')
  const miss: string[] = []
  markers.forEach(function (m) { if (html.indexOf(m) < 0) miss.push(m) })
  return miss
}

/** True when at least one priced model offers a channel choice. */
export function hasVariants(data: any): boolean {
  const models = ((data || {}).models || []) as any[]
  return models.some(function (m) {
    const meta = ((data.pricing || {})[m.key] || {}) as any
    const vs = meta.variants || []
    return vs.length > 1
  })
}

/** 总览 Tab:cost-hero + min/avg/max + 范围过滤趋势 + 范围化跨工具(全部按 range 实时算) */
