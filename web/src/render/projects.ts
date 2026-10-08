import {
  fmt, money, pct, CATS, POLICIES, SOURCES,
  normalizeCost, costOfTokens, computeAll, compareSources,
  dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor, mergeDailyCost, monthToDateCost,
  filterHoursByRange, hourMatrix, rangeInsights, RANGES,
  filterDaysByRange, rangeCutoffKey, calcStreak, weekTopModels,
  type PricingOpts, type PriceSource, type RangeKey
} from '../pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, heatmap, trimNum } from '../charts'
import { th, sourceTag, stableColorIndex, sesRecency, pad2, localDay, esc, emptyStateHtml, type UiState } from './shared'

// 分级阈值是唯一真相源，设置页那句「会在 X% 换色」也从这里生成 ——
// 旧稿把 50/80/95 写死在设置页文案里、分级逻辑另写一份，加一档就漂一档。
export const BUDGET_TIERS = [50, 80, 95] as const

// 四档分级。旧稿 >=95 与 >=80 都归 'err'，两个分支完全等价 —— 95% 那一档是死代码，
// 而设置页的说明文字还写着「50% / 80% / 95% 分级提示」，界面承诺了一个它没有提供的
// 分辨率：80% 和 99% 在屏幕上长得一模一样，用户会以为红色就等于 95%。
// 现在 95% 单独一档，各档文案也说实话。
export function budgetAlertHtml(cost: number, budgetUsd: number): string {
  if (!budgetUsd || budgetUsd <= 0) return ''
  const pctUsed = cost / budgetUsd * 100
  const level = pctUsed >= BUDGET_TIERS[2] ? 'err2' : pctUsed >= BUDGET_TIERS[1] ? 'err'
    : pctUsed >= BUDGET_TIERS[0] ? 'warn' : 'ok'
  const tag = level === 'err2' ? '预算已超'
    : level === 'err' ? '注意 · 预算快用完'
      : level === 'warn' ? '注意 · 预算过半'
        : '预算进度'
  const tail = level === 'err2' ? '本月至今已超出月预算。'
    : level === 'err' ? '本月至今已用掉八成以上，可以现在调整。'
      : level === 'warn' ? '本月至今已过半。'
        : '本月至今进度。在设置页可调整月预算。'
  return '<div class="note budget-' + level + '"><b>' + tag + '</b>：本月至今已用 <b>' + money(cost) +
    '</b> / 预算 <b>' + money(budgetUsd) + '</b>（' + pct(pctUsed) + '）' + tail + '</div>'
}

/**
 * git 产出与用量归因。
 *
 * data.yield 的项目名来自 git root 扫描（仓库目录 basename），用量侧的
 * sessions.project_id 是会话自己带的项目名 —— 两边没有 join key。所以：
 *  - 同名对上 → 才给费用与「每次提交成本」；
 *  - 对不上 → 标「未归因」，绝不显示 0 冒充。
 */
export interface YieldRow {
  name: string
  root: string
  total: number
  lastDay: string | null
  lastSubject: string | null
  /** 与之同名、且在当前项目视图里有费用的项目名；对不上为 null */
  attributedTo: string | null
}

export function yieldRows(data: any, projectNames: Record<string, true>): YieldRow[] {
  const ys = (data || {}).yield
  if (!Array.isArray(ys)) return []
  return ys.map(function (y: any) {
    const days = (y.days || []).slice().sort(function (a: any, b: any) { return a.d < b.d ? 1 : -1 })
    const last = days[0]
    const name = String(y.project || '')
    return {
      name,
      root: String(y.root || ''),
      total: Number(y.total || 0),
      lastDay: last ? String(last.d) : null,
      lastSubject: last && last.subjects && last.subjects[0] ? String(last.subjects[0]) : null,
      attributedTo: projectNames[name] ? name : null
    }
  })
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
  const projNames: Record<string, true> = {}
  projRows.forEach(function (p) { projNames[p.name] = true })
  const allYield = yieldRows(data, projNames)
  const withCommits = allYield.filter(function (y: YieldRow) { return y.total > 0 })
  const noCommits = allYield.filter(function (y: YieldRow) { return y.total <= 0 })

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

  /* 预算条。与设置页同一个口径：全工具、本月至今、与范围无关。
     旧稿这里传的是范围费用（all 时更是全量），和设置页的「月预算」对不上。 */
  const ba = budgetAlertHtml(monthToDateCost(mergeDailyCost(
    dailyCost(data, s), externalSummary(data, st.policy), data)), st.budgetUsd)
  if (ba) html.push(ba)

  /* 归因条 */
  html.push('<section class="card">')
  html.push('<h2>' + esc(rangeLabel) + ' 项目归因 <span class="hint">' +
    (approx ? '范围内按会话 updatedAt 过滤 · token/费用为会话级近似 · ' : '') +
    '点击条目钻取该项目</span></h2>')
  if (!projRows.length) {
    const lastDay = allSess.reduce(function (m: string, x: any) {
      const ts = x.updatedAt || x.createdAt
      if (!ts) return m
      const d = localDay(ts)
      return d > m ? d : m
    }, '')
    html.push(emptyStateHtml({
      tab: 'projects', rangeLabel, what: '项目用量',
      allCount: allSess.length, lastDay: lastDay || null
    }))
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

  /* 产出 · git 汇总：首层只给一行口径 + 仓库清单，有提交数的在前，
     0 提交的折进 <details>，不占首层版面（旧稿在总览一次铺 29 行）。 */
  if (allYield.length) {
    const attributed = withCommits.filter(function (y: YieldRow) { return !!y.attributedTo })
    const unattributed = withCommits.filter(function (y: YieldRow) { return !y.attributedTo })
    const commitTotal = allYield.reduce(function (t: number, y: YieldRow) { return t + y.total }, 0)
    html.push('<section class="card">')
    html.push('<h2>产出 · git <span class="hint">本地仓库 commit · ' +
      allYield.length + ' 个仓库共 ' + fmt(commitTotal) + ' 次提交</span></h2>')
    html.push('<div class="note-inline dim small">归因规则：git 表的项目名来自仓库目录名（<code>y.root</code>），' +
      '用量来自 <code>sessions.project_id</code>，两边没有 join key —— 只有<strong>同名</strong>才对得上费用。' +
      '对不上的一律记「未归因」，不按 0 元计，也不摊「每次提交成本」。' +
      '点上面任一项目进钻取，可看该项目对上的仓库与每次提交成本。</div>')
    html.push('<div class="tbl-wrap"><table class="tbl"><thead><tr>' +
      '<th>仓库</th><th class="num">提交</th><th>归因</th><th>最近提交</th>' +
      '</tr></thead><tbody>')
    withCommits.slice().sort(function (a: any, b: any) { return b.total - a.total }).forEach(function (y: YieldRow) {
      html.push('<tr' + (y.attributedTo ? ' data-proj="' + esc(y.attributedTo) + '"' : '') + '>' +
        '<td class="mname"><b>' + esc(y.name) + '</b><div class="raw">' + esc(y.root) + '</div></td>' +
        '<td class="num strong">' + fmt(y.total) + '</td>' +
        '<td>' + (y.attributedTo
          ? '<span class="tag tag-ok">对上 ' + esc(y.attributedTo) + '</span>'
          : '<span class="tag tag-miss">未归因</span><span class="dim small"> 无用量数据</span>') + '</td>' +
        '<td class="num">' + esc((y.lastDay || '—') + (y.lastSubject ? ' · ' + y.lastSubject : '')) + '</td></tr>')
    })
    html.push('</tbody></table></div>')
    html.push('<div class="note-inline dim small">其中 ' + attributed.length + ' 个仓库与项目同名（可归因），' +
      unattributed.length + ' 个未归因（无 join key，不摊成本）。</div>')
    if (noCommits.length) {
      html.push('<details class="yield-zero"><summary>' + noCommits.length +
        ' 个仓库没有提交（不在上表；不是 0 元，只表示没扫到 commit）</summary>' +
        '<div class="dim small">' + noCommits.map(function (y: YieldRow) {
          return esc(y.name) + (y.attributedTo ? '（对得上 ' + esc(y.attributedTo) + '）' : '')
        }).join('、') + '</div></details>')
    }
    html.push('</section>')
  }

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

    /* 产出 · git（从总览下沉到这里：它得挂在「项目」语境下才谈得上成本） */
    {
      const ys = yieldRows(data, projNames).filter(function (y: YieldRow) { return y.name === pname })
      html.push('<section class="card">')
      html.push('<h2>该项目产出 · git <span class="hint">本地仓库 commit · 归因按仓库名与项目名同名匹配</span></h2>')
      if (!ys.length) {
        html.push('<div class="empty">没有扫到与「' + esc(pname) + '」同名的 git 仓库 —— ' +
          '该项目的用量有 ' + money(pCost) + ' 费用但无提交可归因（未归因），' +
          '不是「0 次提交」。</div>')
      } else {
        const ysCommits = ys.reduce(function (t: number, y: YieldRow) { return t + y.total }, 0)
        html.push('<div class="tbl-wrap"><table class="tbl"><thead><tr>' +
          '<th>仓库</th><th class="num">提交</th><th class="num">每次提交成本</th><th>最近提交</th>' +
          '</tr></thead><tbody>')
        ys.forEach(function (y: YieldRow) {
          const perCommit = y.total > 0 ? pCost / y.total : null
          html.push('<tr><td class="mname"><b>' + esc(y.name) + '</b><div class="raw">' + esc(y.root) + '</div></td>' +
            '<td class="num strong">' + fmt(y.total) + '</td>' +
            '<td class="num strong">' + (perCommit === null
              ? '<span class="dim">无提交，无法摊算</span>'
              : money(perCommit)) + '</td>' +
            '<td class="num">' + esc((y.lastDay || '—') + (y.lastSubject ? ' · ' + y.lastSubject : '')) + '</td></tr>')
        })
        html.push('</tbody></table></div>')
        html.push('<div class="note-inline dim small">' + (ysCommits > 0
          ? '「每次提交成本」= ' + esc(pname) + ' 在' + esc(rangeLabel) + '的费用 ' + money(pCost) +
            ' ÷ 该仓库 ' + ysCommits + ' 次提交。仓库名与项目名同名才归因，对不上一律标未归因，不按 0 计。'
          : esc(pname) + ' 在' + esc(rangeLabel) + '有 ' + money(pCost) + ' 费用，但同名仓库扫不到任何提交 —— ' +
            '这是「有费用、无提交」，不是 0 成本。') + '</div>')
      }
      html.push('</section>')
    }

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
