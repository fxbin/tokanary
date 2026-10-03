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
import { budgetAlertHtml } from './projects'

export function renderSettings(data: any, st: UiState, cmp: Record<string, number>, _range: RangeKey, ioText: string): string {
  if (!data) {
    return '<div class="empty">没有用量数据 —— 请先运行 <code>tokanary refresh</code> 重建仓库。</div>'
  }
  const s = computeAll(data, st)
  const costByDay = dailyCost(data, s)
  const rs = rangeStats(data, s, costByDay, _range)
  const html: string[] = []
  const rangeLabel = (RANGES[_range] || RANGES.all).label

  /* 预算告警设置 */
  const ba = budgetAlertHtml(rs.cost, st.budgetUsd)
  html.push('<section class="card">')
  html.push('<h2>预算告警 <span class="hint">按当前范围（' + esc(rangeLabel) + '）费用对比月预算 · 0 = 关闭</span></h2>')
  html.push('<div class="settings">')
  html.push('<div class="set-row"><span>月预算（USD）：</span>' +
    '<input id="budget-usd" type="number" min="0" step="1" value="' + (st.budgetUsd || 0) + '">' +
    '<span class="dim small">' + (st.budgetUsd > 0
      ? rangeLabel + '费用 ' + money(rs.cost) + ' · 占预算 ' + pct(rs.cost / st.budgetUsd * 100)
      : '未启用。填入正数后总览与本页顶部会显示 50% / 80% / 95% 分级提示。') + '</span></div>')
  if (ba) html.push(ba)
  else if (st.budgetUsd > 0) {
    html.push('<div class="note budget-ok"><b>预算进度</b>：' + rangeLabel + ' <b>' + money(rs.cost) +
      '</b> / <b>' + money(st.budgetUsd) + '</b>（' + pct(rs.cost / st.budgetUsd * 100) + '）· 安全</div>')
  }
  html.push('</div></section>')

  /* 价格设置(与 renderPage 区块同构) */
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

  /* 网关价目表(与 renderPage 区块 7 同构) */
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
      '网关未收录的模型在账单里会回退到 models.dev 价格。' +
      '</div>')
    html.push('</section>')
  }

  html.push('<div class="note"><b>设置持久化</b>：口径 / 策略 / 改价 / 自定义源 / 预算 存在 localStorage ' +
    '（键 <code>pi-token-pricing-v1</code>），刷新不丢。导入导出只覆盖价格相关字段，不动预算与主题。</div>')
  return html.join('')
}
