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

export function budgetAlertHtml(cost: number, budgetUsd: number): string {
  if (!budgetUsd || budgetUsd <= 0) return ''
  const pctUsed = cost / budgetUsd * 100
  const level = pctUsed >= 95 ? 'err' : pctUsed >= 80 ? 'err' : pctUsed >= 50 ? 'warn' : 'ok'
  const tag = level === 'err' ? '⚠️ 预算告警' : level === 'warn' ? '⚠️ 预算过半' : '预算进度'
  return '<div class="note budget-' + level + '"><b>' + tag + '</b>：已用 <b>' + money(cost) +
    '</b> / 预算 <b>' + money(budgetUsd) + '</b>（' + pct(pctUsed) + '）' +
    (level === 'ok' ? '。在设置页可调整月预算。' : '。请检查模型用量或提高预算。') + '</div>'
}

/** Tab 项目:归因堆叠条 + 点击钻取(模型/会话/日趋势)。 */
export function renderProjects(data: any, st: UiState, cmp: Record<string, number>, range: RangeKey): string {
  if (!data) {
    return '<div class="empty">没有用量数据 —— 请先运行 <code>tokanary refresh</code> 重建仓库。</div>'
  }
  const s = computeAll(data, st)
  const unitByKey: Record<string, number> = {}
  s.rows.forEach(function (r: any) { unitByKey[r.m.key] = r.unit })
  const allSess: any[] = (data.sessionsAll && data.sessionsAll.length)
    ? data.sessionsAll : (data.sessions || [])
  const anchor = rangeAnchor(data)
  const fdays = filterDaysByRange(data.days || [], range, anchor)
  const allow: Record<string, true> = {}
  fdays.forEach(function (d) { allow[d.d] = true })
  const cut = range === 'all' ? null : rangeCutoffKey(fdays.map(function (d) { return d.d }), range, anchor)
  const maxD = anchor || (fdays.length ? fdays[fdays.length - 1].d : null)
  const inWindow = function (ts: number | null | undefined): boolean {
    if (range === 'all' || cut === null) return true
    if (!ts) return false
    const day = localDay(ts)
    return day >= cut && (!maxD || day <= maxD)
  }
  const rangeLabel = (RANGES[range] || RANGES.all).label
  const approx = range !== 'all'

  /* 归因行:全量用 projects;范围内用 sessionsAll 过滤聚合(token 近似按会话) */
  interface ProgRow {
    name: string; sessions: number; turns: number
    cacheRead: number; cacheWrite: number; input: number; output: number; total: number
    cost: number
  }
  let projRows: ProgRow[]
  if (!approx) {
    projRows = (data.projects || []).filter(function (p: any) { return p.total > 0 }).map(function (p: any) {
      const t = p.total || 0
      const share = t > 0 ? t / t : 1
      void share
      return {
        name: p.name, sessions: p.sessions, turns: p.turns,
        cacheRead: p.cacheRead, cacheWrite: p.cacheWrite, input: p.input, output: p.output,
        total: t, cost: 0
      }
    })
    /* 费用按项目会话 total × 综合均价摊算(与 sessions 同法) */
    const byProj: Record<string, number> = {}
    allSess.forEach(function (x: any) {
      const k = x.project || '(无项目)'
      byProj[k] = (byProj[k] || 0) + (x.total || 0)
    })
    projRows.forEach(function (r) {
      /* 项目无专属 unit:按该名下会话的模型加权近似 */
      let cost = 0
      allSess.forEach(function (x: any) {
        if ((x.project || '(无项目)') !== r.name) return
        cost += (unitByKey[x.model] || 0) * (x.total || 0) / 1e6
      })
      r.cost = cost
    })
  } else {
    const map: Record<string, ProgRow> = {}
    allSess.forEach(function (x: any) {
      const ts = x.updatedAt || x.createdAt
      if (!inWindow(ts)) return
      const k = x.project || '(无项目)'
      let e = map[k]
      if (!e) {
        e = map[k] = {
          name: k, sessions: 0, turns: 0,
          cacheRead: 0, cacheWrite: 0, input: 0, output: 0, total: 0, cost: 0
        }
      }
      e.sessions += 1
      e.turns += x.turns || 0
      e.cacheRead += x.cacheRead || 0
      e.cacheWrite += x.cacheWrite || 0
      e.input += x.input || 0
      e.output += x.output || 0
      e.total += x.total || 0
      e.cost += (unitByKey[x.model] || 0) * (x.total || 0) / 1e6
    })
    projRows = Object.keys(map).map(function (k) { return map[k] })
  }
  projRows.sort(function (a, b) { return b.total - a.total })

  const html: string[] = []
  const sumTok = projRows.reduce(function (t: number, r: any) { return t + r.total }, 0)
  const sumCost = projRows.reduce(function (t: number, r: any) { return t + r.cost }, 0)
  html.push('<section class="kpis">')
  html.push([
    { label: rangeLabel + '项目', value: String(projRows.length), sub: approx ? '会话级近似聚合' : '来自 sessions.project_id' },
    { label: rangeLabel + 'Token', value: fmt(sumTok), sub: '各项目求和' },
    { label: rangeLabel + '费用', value: money(sumCost), sub: '会话 × 模型均价摊算' },
    { label: '钻取', value: st.drillProject || '—', sub: '点击归因条进入项目详情' }
  ].map(function (k) {
    return '<div class="kpi"><div class="kpi-l">' + esc(k.label) +
      '</div><div class="kpi-v">' + esc(k.value) + '</div><div class="kpi-s">' + esc(k.sub) + '</div></div>'
  }).join(''))
  html.push('</section>')

  /* 预算条 */
  const ba = budgetAlertHtml(range === 'all' ? (s.totalCost || sumCost) : sumCost, st.budgetUsd)
  if (ba) html.push(ba)

  /* 归因条 */
  html.push('<section class="card">')
  html.push('<h2>' + esc(rangeLabel) + ' 项目归因 <span class="hint">' +
    (approx ? '范围内按会话 updatedAt 过滤 · token/费用为会话级近似 · ' : '') +
    '点击条目钻取该项目</span></h2>')
  if (!projRows.length) {
    html.push('<div class="empty">当前范围内没有项目用量 —— 换更大时间范围试试。</div>')
  } else {
    const bars = projRows.map(function (p) {
      return {
        label: p.name, sub: p.sessions + ' 会话 · ' + p.turns + ' 轮 · ' + money(p.cost),
        total: p.total, totalText: fmt(p.total),
        segments: CATS.map(function (c) { return { v: (p as any)[c.k] || 0, color: c.color, name: c.label } })
      }
    })
    html.push('<div data-proj-list>' + stackBar(bars, { labelW: 160 }) + '</div>')
    html.push('<div class="proj-rows">' + projRows.map(function (p) {
      return '<button type="button" class="proj-row' + (st.drillProject === p.name ? ' on' : '') +
        '" data-proj="' + esc(p.name) + '">' +
        '<b>' + esc(p.name) + '</b><span class="dim">' + p.sessions + ' 会话</span>' +
        '<span class="num">' + fmt(p.total) + '</span><span class="strong">' + money(p.cost) + '</span></button>'
    }).join('') + '</div>')
  }
  html.push('</section>')

  /* 钻取面板 */
  if (st.drillProject) {
    const pname = st.drillProject
    const pSess = allSess.filter(function (x: any) {
      const ts = x.updatedAt || x.createdAt
      if (!inWindow(ts)) return false
      return (x.project || '(无项目)') === pname
    })
    const pTok = pSess.reduce(function (t: number, x: any) { return t + (x.total || 0) }, 0)
    let pCost = 0
    pSess.forEach(function (x: any) { pCost += (unitByKey[x.model] || 0) * (x.total || 0) / 1e6 })
    const pTurns = pSess.reduce(function (t: number, x: any) { return t + (x.turns || 0) }, 0)

    /* 模型构成 */
    const byM: Record<string, { total: number; fields: any }> = {}
    pSess.forEach(function (x: any) {
      const k = x.model || '(未知)'
      let e = byM[k]
      if (!e) e = byM[k] = { total: 0, fields: { cacheRead: 0, cacheWrite: 0, input: 0, output: 0 } }
      e.total += x.total || 0
      e.fields.cacheRead += x.cacheRead || 0
      e.fields.cacheWrite += x.cacheWrite || 0
      e.fields.input += x.input || 0
      e.fields.output += x.output || 0
    })
    const mBars = Object.keys(byM).map(function (k) {
      const e = byM[k]
      return {
        label: k, sub: money((unitByKey[k] || 0) * e.total / 1e6),
        total: e.total, totalText: fmt(e.total),
        segments: CATS.map(function (c) { return { v: (e.fields as any)[c.k] || 0, color: c.color, name: c.label } })
      }
    }).filter(function (b) { return b.total > 0 })
    mBars.sort(function (a, b) { return b.total - a.total })

    /* 日趋势:按会话更新日近似 */
    const byDay: Record<string, number> = {}
    pSess.forEach(function (x: any) {
      const ts = x.updatedAt || x.createdAt
      if (!ts) return
      const d = localDay(ts)
      byDay[d] = (byDay[d] || 0) + (x.total || 0)
    })
    const dayKeys = Object.keys(byDay).sort()
    const dayD = dayKeys.map(function (d) {
      const total = byDay[d]
      return {
        d, cacheRead: 0, cacheWrite: 0, input: 0, output: 0, total
      }
    })
    const costByDay: Record<string, number> = {}
    dayKeys.forEach(function (d) { costByDay[d] = 0 })
    pSess.forEach(function (x: any) {
      const ts = x.updatedAt || x.createdAt
      if (!ts) return
      const d = localDay(ts)
      costByDay[d] += (unitByKey[x.model] || 0) * (x.total || 0) / 1e6
    })

    html.push('<section class="card drill">')
    html.push('<h2>项目钻取 · ' + esc(pname) + ' <span class="hint">' + esc(rangeLabel) +
      ' · 日趋势按会话更新日近似（无项目级 dayModel）</span>' +
      '<button type="button" id="drill-back" class="drill-back">← 返回全部项目</button></h2>')
    html.push('<section class="kpis">')
    html.push([
      { label: 'Token', value: fmt(pTok), sub: pSess.length + ' 会话' },
      { label: '费用', value: money(pCost), sub: '摊算' },
      { label: '轮次', value: String(pTurns), sub: '会话求和' },
      { label: '模型数', value: String(mBars.length), sub: '该项目用过' }
    ].map(function (k) {
      return '<div class="kpi"><div class="kpi-l">' + esc(k.label) +
        '</div><div class="kpi-v">' + esc(k.value) + '</div><div class="kpi-s">' + esc(k.sub) + '</div></div>'
    }).join(''))
    html.push('</section>')

    html.push('<section class="card">')
    html.push('<h2>该项目模型构成 <span class="hint">堆叠 = 缓存读/写/输入/输出</span></h2>')
    if (mBars.length) {
      html.push(stackBar(mBars, { labelW: 170 }))
      html.push(legend(CATS.map(function (c) { return { label: c.label, color: c.color } })))
    } else {
      html.push('<div class="empty">该项目在当前范围内没有模型用量。</div>')
    }
    html.push('</section>')

    html.push('<section class="card">')
    html.push('<h2>该项目日趋势 <span class="hint">按会话更新日聚合 token（近似）</span></h2>')
    if (dayD.length) {
      html.push(dayChart(dayD, costByDay))
    } else {
      html.push('<div class="empty">该项目在当前范围内没有会话活动。</div>')
    }
    html.push('</section>')

    html.push('<section class="card">')
    html.push('<h2>该项目会话 <span class="hint">最多列 ' + Math.min(pSess.length, 40) + ' / ' + pSess.length + ' 条</span></h2>')
    if (!pSess.length) {
      html.push('<div class="empty">该项目在当前范围内没有会话。</div>')
    } else {
      const sorted = pSess.slice().sort(function (a: any, b: any) {
        return (b.updatedAt || 0) - (a.updatedAt || 0)
      }).slice(0, 40)
      html.push('<div class="tbl-wrap"><table class="tbl"><thead><tr><th>#</th><th>标题</th><th>模型</th>' +
        '<th class="num">轮次</th><th class="num">合计 Token</th><th class="num">输出</th><th>更新</th></tr></thead><tbody>')
      sorted.forEach(function (x: any, i: number) {
        html.push('<tr><td class="num">' + (i + 1) + '</td>' +
          '<td class="mname">' + esc(x.title || '(无标题)') + '</td>' +
          '<td><code>' + esc(x.model || '—') + '</code></td>' +
          '<td class="num">' + (x.turns || 0) + '</td>' +
          '<td class="num strong">' + fmt(x.total || 0) + '</td>' +
          '<td class="num">' + fmt(x.output || 0) + '</td>' +
          '<td class="num dim">' + esc(sesRecency(x.updatedAt || x.createdAt)) + '</td></tr>')
      })
      html.push('</tbody></table></div>')
    }
    html.push('</section>')
    html.push('</section>')
  }

  html.push('<div class="note"><b>项目口径</b>：归因来自 <code>sessions.project_id</code>' +
    (approx ? '；当前范围非全部时按会话 <code>updatedAt</code> 过滤后重聚（token/费用为会话级近似，非 warehouse 精确 dayModel）' : '') +
    '。钻取日趋势按会话更新日聚合。全量 models.dev <b>' + money(cmp.modelsdev) + '</b>。</div>')
  return html.join('')
}

/** Tab 设置:价格设置 + 预算告警 + 导入导出(与 renderPage 区块同构)。 */
