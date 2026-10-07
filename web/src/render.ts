/* 整页 HTML 构造器 —— 分区块模块 + 页面装配。 */
import {
  fmt, money, pct, CATS, POLICIES, SOURCES,
  normalizeCost, costOfTokens, computeAll, compareSources,
  dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor,
  filterHoursByRange, hourMatrix, rangeInsights, RANGES,
  filterDaysByRange, rangeCutoffKey, calcStreak, weekTopModels,
  type PricingOpts, type PriceSource, type RangeKey
} from './pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, heatmap, trimNum } from './charts'
import { defaultUiState, th, sourceTag, esc, type UiState } from './render/shared'
import { renderOverview } from './render/overview'
import { renderModels } from './render/models'
import { renderSessions } from './render/sessions'
import { budgetAlertHtml, renderProjects } from './render/projects'
import { renderSettings } from './render/settings'

export type { UiState } from './render/shared'
export { defaultUiState, esc } from './render/shared'
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
      html.push('<div class="warn-box">注意：其他 CLI 里有 ' + fmt(ext.unpricedTokens) +
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
    html.push('<div class="warn-box">注意：有 ' + s.unpriced.length + ' 个模型没有匹配到 models.dev 价格，涉及 ' +
      fmt(s.unpricedTokens) + ' token（占总量 ' + pct(100 - s.coverage) + '）：' +
      s.unpriced.map(function (r: any) { return '<code>' + esc(r.m.key) + '</code>' }).join('、') +
      '。请在上表对应行填入单价，费用会自动重算。</div>')
  }
  html.push('</section>')

  /* 价格设置：与 renderSettings 同构（单价只走 models.dev，无自定义源、无网关价目表） */
  const policyMatters = s.rows.some(function (r: any) {
    return r.m.cacheWrite > 0 && (r.price.estimatedFields || []).indexOf('cache_write') >= 0
  })
  const matched = s.rows.filter(function (r: any) { return r.price.source !== 'missing' }).length
  html.push('<section class="card">')
  html.push('<h2>价格设置</h2>')
  html.push('<div class="settings">')
  html.push('<div class="set-row"><span>计费口径：</span>' +
    Object.keys(SOURCES).map(function (k) {
      return '<label class="radio"><input type="radio" name="psrc" value="' + k + '"' +
        (st.priceSource === k ? ' checked' : '') + '> ' + esc((SOURCES as any)[k].label) +
        ' <b>' + money((cmp as any)[k]) + '</b></label>'
    }).join('') + '</div>')
  html.push('<label class="set-row"><span>缺失缓存单价时：</span><select id="policy">' +
    Object.keys(POLICIES).map(function (k) {
      return '<option value="' + k + '"' + (k === st.policy ? ' selected' : '') + '>' + esc((POLICIES as any)[k].label) + '</option>'
    }).join('') + '</select>' +
    (policyMatters ? '' :
      '<span class="dim small">当前数据里没有「有缓存写用量但缺单价」的模型，切换此项不影响金额（仅作为以后数据的兜底）</span>') +
    '</label>')
  // 旧稿这行无条件兜底渲染「网关价目表 自有网关 · 抓取于 未知」，凭空声明一个不存在的价源。
  const pm = (data.pricingMeta || {})
  const pmSrc = String(pm.source || '')
  html.push('<div class="set-row"><span>价格来源：</span><span class="src-info">' +
    'models.dev' + (pmSrc && pmSrc !== 'models.dev' ? ' ' + esc(pmSrc) : '') +
    ' · 抓取于 ' + esc(pm.fetchedAt || '未知') +
    ' · 本机 ' + s.rows.length + ' 个模型里 ' + matched + ' 个已匹配单价' +
    (s.rows.length > matched ? '，其余 ' + (s.rows.length - matched) + ' 个在表格里填价后计入金额' : '') +
    '</span></div>')
  html.push('<div class="set-actions">' +
    '<button id="btn-reset">重置为 models.dev 价格</button>' +
    '<button id="btn-export">导出手动改价</button>' +
    '<button id="btn-import">导入手动改价</button>' +
    '<button id="btn-reload">重新读取</button>' +
    '</div>')
  html.push('<textarea id="io" placeholder="点「导出手动改价」后 JSON 出现在这里；粘贴 JSON 后点「导入手动改价」">' + esc(ioText) + '</textarea>')
  html.push('</div></section>')


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
