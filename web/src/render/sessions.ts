import {
  fmt, money, pct, CATS, POLICIES, SOURCES,
  normalizeCost, costOfTokens, computeAll, compareSources,
  dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor,
  filterHoursByRange, hourMatrix, rangeInsights, RANGES,
  filterDaysByRange, rangeCutoffKey, calcStreak, weekTopModels,
  type PricingOpts, type PriceSource, type RangeKey
} from '../pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, heatmap, trimNum } from '../charts'
import { th, sourceTag, stableColorIndex, sesRecency, pad2, localDay, esc, emptyStateHtml, type UiState } from './shared'

export function renderSessions(data: any, st: UiState, cmp: Record<string, number>, range: RangeKey): string {
  if (!data) {
    return '<div class="empty">没有用量数据 —— 请先运行 <code>tokanary refresh</code> 重建仓库。</div>'
  }
  const s = computeAll(data, st)
  const costByDay = dailyCost(data, s)
  const rs = rangeStats(data, s, costByDay, range)
  const anchor = rangeAnchor(data)
  const rangeLabel = (RANGES[range] || RANGES.all).label
  const unitByKey: Record<string, number> = {}
  s.rows.forEach(function (r: any) { unitByKey[r.m.key] = r.unit })

  const fdays = filterDaysByRange(data.days || [], range, anchor)
  const allow: Record<string, true> = {}
  fdays.forEach(function (d) { allow[d.d] = true })
  const inDay = function (ts: number | null | undefined): boolean {
    if (range === 'all') return true
    if (!ts) return false
    const cut = rangeCutoffKey(fdays.map(function (d) { return d.d }), range, anchor)
    if (cut === null) return true
    const day = new Date(ts).toISOString().slice(0, 10) // 注意:本地日 vs UTC —— 改用本地
    void day
    const dt = new Date(ts)
    const pad = function (x: number) { return String(x).padStart(2, '0') }
    const local = dt.getFullYear() + '-' + pad(dt.getMonth() + 1) + '-' + pad(dt.getDate())
    const maxD = anchor || cut
    return local >= cut && local <= maxD
  }

  const q = (st.sesQuery || '').trim().toLowerCase()
  let rows = (data.sessions || []).filter(function (x: any) {
    const ts = x.updatedAt || x.createdAt
    if (!inDay(ts)) return false
    if (!q) return true
    return String(x.title || '').toLowerCase().indexOf(q) >= 0
      || String(x.project || '').toLowerCase().indexOf(q) >= 0
      || String(x.model || '').toLowerCase().indexOf(q) >= 0
      || String(x.id || '').toLowerCase().indexOf(q) >= 0
  }).map(function (x: any) {
    const created = x.createdAt || 0
    const updated = x.updatedAt || created || Date.now()
    const days = Math.max(1, Math.ceil((updated - created) / 86400000)) || 1
    const unit = unitByKey[x.model] || 0
    const burnTok = (x.total || 0) / days
    const burnCost = unit * (x.total || 0) / 1e6 / days
    return {
      x, created, updated, days,
      burnTok, burnCost,
      unitCost: unit * (x.total || 0) / 1e6
    }
  })

  const sk = st.sesSortKey, sdir = st.sesSortDir
  rows.sort(function (a: any, b: any) {
    let va: any, vb: any
    if (sk === 'title') {
      va = String(a.x.title || ''); vb = String(b.x.title || '')
      return va < vb ? -sdir : va > vb ? sdir : 0
    }
    if (sk === 'turns') { va = a.x.turns || 0; vb = b.x.turns || 0 }
    else if (sk === 'total') { va = a.x.total || 0; vb = b.x.total || 0 }
    else if (sk === 'output') { va = a.x.output || 0; vb = b.x.output || 0 }
    else if (sk === 'burn') { va = a.burnTok; vb = b.burnTok }
    else { va = a.updated; vb = b.updated }
    return (va - vb) * sdir
  })

  const sesTh = function (key: string, label: string) {
    const arrow = sk === key ? (sdir < 0 ? ' ▾' : ' ▴') : ''
    return '<th class="sortable' + (sk === key ? ' on' : '') + '" data-ses-sort="' + key + '">' + esc(label) + arrow + '</th>'
  }

  const html: string[] = []
  const totalIn = rows.length
  const sumTok = rows.reduce(function (t: number, r: any) { return t + (r.x.total || 0) }, 0)
  const sumBurn = rows.reduce(function (t: number, r: any) { return t + r.burnCost }, 0)

  html.push('<section class="kpis">')
  html.push([
    { label: rangeLabel + '会话', value: String(totalIn), sub: '全量 ' + ((data.sessions || []).length) + ' 个' },
    { label: rangeLabel + 'Token', value: fmt(sumTok), sub: '筛选后求和' },
    { label: '合计 burn', value: money(sumBurn), sub: '各会话 $/天 之和' },
    { label: '搜索', value: q ? '"' + q + '"' : '—', sub: '匹配 title / project / model / id' }
  ].map(function (k) {
    return '<div class="kpi"><div class="kpi-l">' + esc(k.label) +
      '</div><div class="kpi-v">' + esc(k.value) + '</div><div class="kpi-s">' + esc(k.sub) + '</div></div>'
  }).join(''))
  html.push('</section>')

  html.push('<section class="card">')
  html.push('<h2>' + esc(rangeLabel) + ' 会话 <span class="hint">可搜索 · 点表头排序 · burn = token 或 $ / 活跃天数</span></h2>')
  html.push('<div class="gw-bar">' +
    '<input id="ses-search" type="search" placeholder="搜索标题 / 项目 / 模型 / id…" value="' + esc(st.sesQuery) + '">' +
    '<span class="dim small">显示 ' + totalIn + ' / ' + ((data.sessions || []).length) + ' 个</span>' +
    '</div>')

  if (!rows.length) {
    if (q) {
      html.push('<div class="empty">没有匹配「' + esc(q) + '」的会话 —— 换个关键词或扩大时间范围。</div>')
    } else {
      // 「窗口内为 0」和「从来没有数据」必须分开说：pi 侧最新活动日常落后于外部工具
      // （锚点取所有源最新日），手动切到近 7 天就会撞上「数据落在窗口外」。
      const all = data.sessions || []
      const lastDay = all.reduce(function (m: string, x: any) {
        const ts = x.updatedAt || x.createdAt
        if (!ts) return m
        const d = localDay(ts)
        return d > m ? d : m
      }, '')
      html.push(emptyStateHtml({
        tab: 'sessions', rangeLabel, what: '会话',
        allCount: all.length, lastDay: lastDay || null
      }))
    }
  } else {
    html.push('<div class="tbl-wrap"><table class="tbl"><thead><tr>' +
      '<th>#</th>' + sesTh('title', '标题') + '<th>项目</th><th>模型</th>' +
      sesTh('turns', '轮次') + '<th>消息</th>' +
      sesTh('total', '合计 Token') + sesTh('output', '输出') +
      sesTh('burn', 'Burn (tok/天)') + '<th class="num">Burn ($/天)</th><th>活跃天</th>' +
      sesTh('updatedAt', 'Recency') + '</tr></thead><tbody>')
    rows.forEach(function (r: any, i: number) {
      const x = r.x
      html.push('<tr>')
      html.push('<td class="num">' + (i + 1) + '</td>')
      html.push('<td class="mname">' + esc(x.title || '(无标题)') +
        (x.id ? '<div class="raw">' + esc(String(x.id).slice(0, 12)) + '</div>' : '') + '</td>')
      html.push('<td>' + esc(x.project || '(无项目)') + '</td>')
      html.push('<td><code>' + esc(x.model || '—') + '</code></td>')
      html.push('<td class="num">' + (x.turns || 0) + '</td>')
      html.push('<td class="num">' + (x.messages || 0) + '</td>')
      html.push('<td class="num strong">' + fmt(x.total || 0) + '</td>')
      html.push('<td class="num">' + fmt(x.output || 0) + '</td>')
      html.push('<td class="num">' + fmt(r.burnTok) + '</td>')
      html.push('<td class="num">' + (r.unitCost > 0 ? money(r.burnCost) : '—') + '</td>')
      html.push('<td class="num">' + r.days + '</td>')
      html.push('<td class="num dim" title="' + (r.updated ? new Date(r.updated).toLocaleString('zh-CN') : '') + '">' +
        esc(sesRecency(r.updated)) + '</td>')
      html.push('</tr>')
    })
    html.push('</tbody></table></div>')
  }
  html.push('<div class="note-inline dim small">burn 天数 = max(1, ceil((updatedAt−createdAt)/1天))；' +
    '$/天 = 范围内该模型综合均价 × total / 天数（与总览摊算同法，随口径实时变）。</div>')
  html.push('</section>')

  html.push('<div class="note"><b>范围内</b>：按会话 <code>updatedAt</code> 落在 ' + esc(rangeLabel) +
    ' 窗口过滤；全量 models.dev <b>' + money(cmp.modelsdev) + '</b>。</div>')
  void rs
  return html.join('')
}

