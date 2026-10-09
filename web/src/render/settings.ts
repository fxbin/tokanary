import {
  fmt, money, pct, CATS, POLICIES, SOURCES, normalizeCost, costOfTokens, computeAll,
  compareSources, dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor, mergeDailyCost,
  monthToDateCost, filterHoursByRange, hourMatrix, rangeInsights, RANGES, filterDaysByRange,
  rangeCutoffKey, calcStreak, weekTopModels, type PricingOpts, type PriceSource, type RangeKey
} from '../pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, trimNum } from '../charts'
import { th, sourceTag, stableColorIndex, sesRecency, pad2, localDay, esc, type UiState } from './shared'
import { budgetAlertHtml, BUDGET_SCOPE_LABEL, BUDGET_TIERS } from './projects'

export function renderSettings(data: any, st: UiState, cmp: Record<string, number>, _range: RangeKey, ioText: string, ioError = ''): string {
  if (!data) {
    return '<div class="empty">没有用量数据 —— 请先运行 <code>tokanary refresh</code> 重建仓库。</div>'
  }
  const s = computeAll(data, st)
  const rangeLabel = (RANGES[_range] || RANGES.all).label
  const piByDay = dailyCost(data, s)
  const ext = externalSummary(data, st.policy)
  // 预算必须比全工具费用：顶栏与跨工具总览报的都是全工具数，拿纯 pi 侧费用去
  // 和它对照，等于让预算对不上用户真正在意的花钱量（pi 一停更就永远显示 0%）。
  const costByDay = mergeDailyCost(piByDay, ext, data)
  const rs = rangeStats(data, s, piByDay, _range)
  const mtd = monthToDateCost(costByDay)
  const html: string[] = []

  /* 预算告警设置。分子是本月至今、分母是月预算，都与顶栏的范围切换无关 ——
     旧稿拿「当前范围费用」除月预算，切一次范围结论就翻转一次。 */
  const ba = budgetAlertHtml(mtd, st.budgetUsd, BUDGET_SCOPE_LABEL)
  html.push('<section class="card">')
  html.push('<h2>预算告警 <span class="hint">本月至今（全工具）费用对比月预算 · 不随上方范围切换 · 0 = 关闭</span></h2>')
  html.push('<div class="settings">')
  html.push('<div class="set-row"><span>月预算（USD）：</span>' +
    '<input id="budget-usd" type="number" min="0" step="1" value="' + (st.budgetUsd > 0 ? st.budgetUsd : '') + '">' +
    '<span class="dim small">' + (st.budgetUsd > 0
      ? BUDGET_SCOPE_LABEL + ' ' + money(mtd) + ' · 占预算 ' + pct(mtd / st.budgetUsd * 100)
      : '未启用。填入正数后，本页与总览会在 ' + BUDGET_TIERS.join('%、') + '% 三处换上不同深浅的提示色。') + '</span></div>')
  if (ba) html.push(ba)
  else if (st.budgetUsd > 0) {
    html.push('<div class="note budget-ok"><b>预算进度</b>：' + BUDGET_SCOPE_LABEL + ' <b>' + money(mtd) +
      '</b> / <b>' + money(st.budgetUsd) + '</b>（' + pct(mtd / st.budgetUsd * 100) + '）· 安全</div>')
  }
  html.push('</div></section>')

  /* 价格设置：单价只走 models.dev（AGENTS.md：无网关价源，无自定义源） */
  // 这个设置的作用域是全工具费用，不是只有 pi：外部侧走 externalSummary(data, policy)
  // 也是吃 policy 的，而外部侧占了绝大部分 token。旧稿只扫 s.rows（computeAll 只映射
  // data.models，即 pi 侧），于是在外部模型缺缓存写单价时那句「切换此项不影响金额」
  // 是假的 —— 会骗人的安抚句比没有这句更糟。
  const piNeedsPolicy = s.rows.some(function (r: any) {
    return r.m.cacheWrite > 0 && (r.price.estimatedFields || []).indexOf('cache_write') >= 0
  })
  const extNeedsPolicy = ((ext && ext.rows) || []).some(function (r: any) {
    return (r.models || []).some(function (m: any) {
      return (m.tok && m.tok.cacheWrite || 0) > 0 &&
        (m.estimatedFields || []).indexOf('cache_write') >= 0
    })
  })
  const policyMatters = piNeedsPolicy || extNeedsPolicy
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
  /* 只读陈述，不是设置行：用 .note 而非 .set-row。这行没有可调项，却和输入框
   * 同视觉权重，扫一眼会以为能改 —— 界面暗示了兑现不了的承诺（§7 铁律 3）。 */
  html.push('<div class="note src-note">单价来源：' +
    esc((SOURCES as any)[srcKey].label) +
    ' · <b>' + money((cmp as any)[srcKey]) + '</b>' +
    ' · 抓取于 ' + esc(pm.fetchedAt || '未知') +
    ' · 本机 ' + s.rows.length + ' 个模型里 ' + matched + ' 个已匹配单价' +
    (s.rows.length > matched ? '，其余 ' + (s.rows.length - matched) + ' 个在模型页填价后计入金额' : '') +
    (pmSrc && pmSrc !== 'models.dev' ? ' · 底表源 <code>' + esc(pmSrc) + '</code>' : '') +
    (pm.file ? ' · 底表来自 <code>' + esc(pm.file) + '</code>' : '') +
    '</div>')
  html.push('<label class="set-row"><span>缺失缓存单价时：</span><select id="policy">' +
    Object.keys(POLICIES).map(function (k) {
      return '<option value="' + k + '"' + (k === st.policy ? ' selected' : '') + '>' + esc((POLICIES as any)[k].label) + '</option>'
    }).join('') + '</select>' +
    (policyMatters ? '' :
      '<span class="dim small">当前数据里没有「有缓存写用量但缺单价」的模型，切换此项不影响金额（仅作为以后数据的兜底）</span>') +
    '</label>')
  /* 破坏性动作：覆盖值为空时不渲染。按钮存在却点了写回同一个值，等于界面
   * 承诺了一件不会发生的事（§7 铁律 3）。 */
  html.push('<div class="set-actions">' +
    (ovCount > 0
      ? '<button id="btn-reset">重置为 models.dev 价格（将清除 ' + ovCount + ' 条手动改价）</button>'
      : '') +
    '<button id="btn-export">导出手动改价</button>' +
    '<button id="btn-import">导入手动改价</button>' +
    '<button id="btn-reload">重新读取</button>' +
    '</div>')
  /* 撤销只在真的清空过之后才出现 —— 没有撤销可给的时候不渲染（Nielsen：
     * 用户必须始终看得见系统状态并保有控制权）。 */
  html.push('<div id="undo-slot">' +
    (st.undoPendingCount > 0
      ? '<div class="note">已清除 ' + st.undoPendingCount + ' 条手动改价。' +
        '<button id="btn-undo" class="link-btn">撤销</button></div>'
      : '') +
    '</div>')
  html.push('<div class="dim small">导出/导入只含模型页手动改的 ' + ovCount + ' 条覆盖值与缺价策略；' +
    '单价底表来自 models.dev，不在这里填写也不提交到仓库。</div>')
  if (ioError) html.push('<div class="warn-box">注意：' + esc(ioError) + '</div>')
  html.push('<textarea id="io" placeholder="点「导出手动改价」后 JSON 出现在这里；粘贴 JSON 后点「导入手动改价」">' + esc(ioText) + '</textarea>')
  html.push('</div></section>')

  html.push('<div class="note"><b>设置持久化</b>：口径 / 策略 / 改价 / 预算 存在 localStorage ' +
    '（键 <code>pi-token-pricing-v1</code>），刷新不丢。' +
    '导入导出只覆盖价格相关字段，不动预算与主题。</div>')
  return html.join('')
}