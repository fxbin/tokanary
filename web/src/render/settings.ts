import {
  fmt, money, pct, CATS, POLICIES, SOURCES,
  normalizeCost, costOfTokens, computeAll, compareSources,
  dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor,
  filterHoursByRange, hourMatrix, rangeInsights, RANGES,
  filterDaysByRange, rangeCutoffKey, calcStreak, weekTopModels,
  type PricingOpts, type PriceSource, type RangeKey
} from '../pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, heatmap, trimNum } from '../charts'
import { th, sourceTag, stableColorIndex, sesRecency, pad2, localDay, esc, type UiState } from './shared'
import { budgetAlertHtml } from './projects'
import { computeQuotas, quotaPct, type QuotaUsage } from '../quota'

export function renderSettings(data: any, st: UiState, cmp: Record<string, number>, _range: RangeKey, ioText: string, ioError = ''): string {
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
    '<input id="budget-usd" type="number" min="0" step="1" value="' + (st.budgetUsd > 0 ? st.budgetUsd : '') + '">' +
    '<span class="dim small">' + (st.budgetUsd > 0
      ? rangeLabel + '费用 ' + money(rs.cost) + ' · 占预算 ' + pct(rs.cost / st.budgetUsd * 100)
      : '未启用。填入正数后总览与本页顶部会显示 50% / 80% / 95% 分级提示。') + '</span></div>')
  if (ba) html.push(ba)
  else if (st.budgetUsd > 0) {
    html.push('<div class="note budget-ok"><b>预算进度</b>：' + rangeLabel + ' <b>' + money(rs.cost) +
      '</b> / <b>' + money(st.budgetUsd) + '</b>（' + pct(rs.cost / st.budgetUsd * 100) + '）· 安全</div>')
  }
  html.push('</div></section>')

  /* 套餐限额：额度只在这里能设（总览只读）。0 = 未设上限，总览就写「额度上限未设置」，
     不再出现一条编码成 0% 的空进度条配着真实金额。 */
  {
    const quotas = computeQuotas(data, st.quotas, st.policy)
    html.push('<section class="card">')
    html.push('<h2>限额 <span class="hint">按套餐窗口统计外部 CLI 用量 · 0 = 未设上限</span></h2>')
    if (!quotas.length) {
      html.push('<div class="empty">没有可配置的套餐。</div>')
    } else {
      html.push('<div class="settings">')
      quotas.forEach(function (q: QuotaUsage) {
        const p = quotaPct(q)
        // 标签放在各自输入框的正上方（label 包住说明 + input，点文字也能聚焦）。
        // 旧稿把「token 上限」夹在两个框中间横排，读起来像第二个框的标签。
        html.push('<div class="set-row quota-row">')
        html.push('<span class="quota-name">' + esc(q.plan.label) +
          '<i>' + esc(q.plan.window) + '</i></span>')
        html.push('<label class="quota-cell"><span>token 上限</span>' +
          '<input id="quota-tok-' + esc(q.plan.id) + '" class="quota-input" type="number" min="0" step="100000" ' +
          'placeholder="未设上限" value="' + (q.plan.limitTokens > 0 ? q.plan.limitTokens : '') + '"></label>')
        html.push('<label class="quota-cell"><span>USD 上限</span>' +
          '<input id="quota-usd-' + esc(q.plan.id) + '" class="quota-input" type="number" min="0" step="1" ' +
          'placeholder="未设上限" value="' + (q.plan.limitUsd > 0 ? q.plan.limitUsd : '') + '"></label>')
        html.push('<span class="dim small">窗口内已用 ' + fmt(q.tokens) + ' tok · ' + money(q.cost) +
          (p === null ? '（无上限，不算占比）' : ' · 占 ' + Math.round(p) + '%') + '</span>')
        html.push('</div>')
      })
      html.push('<div class="note-inline dim small">留空或填 0 即关闭该条上限；统计窗口以今天为终点向前推，' +
        '与顶栏的范围切换无关。费用按该工具按真实 token 构成加权的综合均价摊算，与总览同口径。</div>')
      html.push('</div>')
    }
    html.push('</section>')
  }

  /* 价格设置：单价只走 models.dev（AGENTS.md：无网关价源，无自定义源） */
  const policyMatters = s.rows.some(function (r: any) {
    return r.m.cacheWrite > 0 && (r.price.estimatedFields || []).indexOf('cache_write') >= 0
  })
  const ovCount = Object.keys(st.overrides || {}).length
  // 「单价来源」与「价格来源」说的是同一件事：单价只有一个源，拆成两行等于让用户
  // 把 models.dev 读两遍，第二行还多一个标签位。合成一行，按「源 → 金额 → 抓取时间
  // → 匹配情况」排下来，信息一条不少。
  const pm = (data.pricingMeta || {})
  const matched = s.rows.filter(function (r: any) { return r.price.source !== 'missing' }).length
  const pmSrc = String(pm.source || '')
  const srcKey = st.priceSource || 'modelsdev'
  html.push('<section class="card">')
  html.push('<h2>价格设置</h2>')
  html.push('<div class="settings">')
  html.push('<div class="set-row"><span>单价来源：</span><span class="src-info">' +
    esc((SOURCES as any)[srcKey].label) +
    ' · <b>' + money((cmp as any)[srcKey]) + '</b>' +
    ' · 抓取于 ' + esc(pm.fetchedAt || '未知') +
    ' · 本机 ' + s.rows.length + ' 个模型里 ' + matched + ' 个已匹配单价' +
    (s.rows.length > matched ? '，其余 ' + (s.rows.length - matched) + ' 个在模型页填价后计入金额' : '') +
    (pmSrc && pmSrc !== 'models.dev' ? ' · 底表源 <code>' + esc(pmSrc) + '</code>' : '') +
    (pm.file ? ' · 底表来自 <code>' + esc(pm.file) + '</code>' : '') +
    '</span></div>')
  html.push('<label class="set-row"><span>缺失缓存单价时：</span><select id="policy">' +
    Object.keys(POLICIES).map(function (k) {
      return '<option value="' + k + '"' + (k === st.policy ? ' selected' : '') + '>' + esc((POLICIES as any)[k].label) + '</option>'
    }).join('') + '</select>' +
    (policyMatters ? '' :
      '<span class="dim small">当前数据里没有「有缓存写用量但缺单价」的模型，切换此项不影响金额（仅作为以后数据的兜底）</span>') +
    '</label>')
  html.push('<div class="set-actions">' +
    '<button id="btn-reset">重置为 models.dev 价格</button>' +
    '<button id="btn-export">导出手动改价</button>' +
    '<button id="btn-import">导入手动改价</button>' +
    '<button id="btn-reload">重新读取</button>' +
    '</div>')
  html.push('<div class="dim small">导出/导入只含模型页手动改的 ' + ovCount + ' 条覆盖值与缺价策略；' +
    '单价底表来自 models.dev，不在这里填写也不提交到仓库。</div>')
  if (ioError) html.push('<div class="warn-box">注意：' + esc(ioError) + '</div>')
  html.push('<textarea id="io" placeholder="点「导出手动改价」后 JSON 出现在这里；粘贴 JSON 后点「导入手动改价」">' + esc(ioText) + '</textarea>')
  html.push('</div></section>')

  html.push('<div class="note"><b>设置持久化</b>：口径 / 策略 / 改价 / 预算 存在 localStorage ' +
    '（键 <code>pi-token-pricing-v1</code>），刷新不丢；套餐额度存在 <code>tokanary-quotas-v1</code>。' +
    '导入导出只覆盖价格相关字段，不动预算、额度与主题。</div>')
  return html.join('')
}