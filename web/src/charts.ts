/* SVG/图例构造器 —— 与仓根 app.js 同名函数逐行对齐的 TS 移植(Slice3b)。
 * 纯字符串构造,无 DOM 依赖,方便单测。
 *
 * 填色约定：本文件的 color 槽位传的是 palette.ts 的 `var(--chart-cN)` 引用而非 hex，
 * 主题切换由 tokens.css 换 token 值完成（夜非反相，勿在业务代码里按主题分支）。
 * 调用方若绕过 palette.ts 传裸 hex，纸/夜就退回同一套颜色。 */
import { esc, fmt, money, pct, CATS } from './pricing'
import { MODEL_COLORS as PALETTE6 } from './palette'

/* 单源:字面量在 palette.ts;render.ts 等仍 from './charts' 取用,调用面不变 */
export const MODEL_COLORS = PALETTE6

export interface StackSeg { v: number; color: string; name: string }
export interface StackItem {
  label: string; sub?: string; total: number; totalText: string; segments: StackSeg[]
}

export function trimNum(v: number): string {
  const n = Number(v)
  if (!isFinite(n)) return ''
  const a = Math.abs(n)
  const dec = a >= 100 ? 2 : a >= 1 ? 3 : a >= 0.01 ? 4 : 6   // 小单价保留更多位
  return String(Number(n.toFixed(dec)))
}

export function dateShort(ts: number): string {
  const d = new Date(ts)
  return (d.getMonth() + 1) + '/' + d.getDate()
}

/* SVG 文字宽度估算（viewBox 单位）。中日韩与全角标点按 1em，其余按 0.55em，
   与 ui.css 的 .lbl(12.5px) / .lbl-sub(10.5px) / .val(12px) 对齐。
   量宽而不是写死 labelW：工具名长度差很多（「pi」与
   「DeepSeek Harness（DSH Desktop）」差 17 倍），写死宽度短的那个就把长标签
   压在柱子上 —— .chart 是 overflow: visible，溢出的文字不会被裁掉，只会盖住柱子。 */
export function textUnits(s: string, fontSize: number): number {
  let u = 0
  const str = String(s == null ? '' : s)
  for (let i = 0; i < str.length; i++) {
    u += /[ᄀ-ᅟ⺀-〾ぁ-㏿㐀-䶿一-鿿ꀀ-꓏가-힣豈-﫿︰-﹏＀-｠￠-￦]/.test(str.charAt(i)) ? 1 : 0.55
  }
  return u * fontSize
}

const LBL_FS = 12.5, SUB_FS = 10.5, VAL_FS = 12

export function stackBar(items: StackItem[], opts: { rowH?: number; labelW?: number; rightW?: number } = {}): string {
  // items: [{label, sub, segments:[{v,color,name}], totalText}]
  const rowH = opts.rowH || 34, gap = 10
  const w = 900
  const PAD = 12
  // 调用方传的 labelW/rightW 只当下限；实测不够时按内容撑开
  let needLabel = opts.labelW || 0
  let needRight = opts.rightW || 0
  for (const it of items) {
    needLabel = Math.max(needLabel, textUnits(it.label, LBL_FS))
    if (it.sub) needLabel = Math.max(needLabel, textUnits(it.sub, SUB_FS))
    needRight = Math.max(needRight, textUnits(it.totalText, VAL_FS))
  }
  // 标签栏最多占三分之一，否则短条的绘图区被挤没；超出部分截断加省略号
  const labelCap = w * 0.34
  let labelW = Math.min(needLabel + PAD, labelCap)
  const rightW = Math.min(needRight + PAD, w * 0.28)
  const clipLabel = function (s: string, fs: number): string {
    return textUnits(s, fs) > labelW - PAD ? s.slice(0, Math.max(1, Math.floor((labelW - PAD) / fs))) + '…' : s
  }
  const plotW = w - labelW - rightW
  const h = items.length * (rowH + gap) + 14
  const segSumOf = function (it: StackItem): number {
    return it.segments.reduce(function (t, s) { return t + Math.max(0, s.v) }, 0)
  }
  // 条长按 max(total, 分段和) 归一:外部 days 缺 total 时分段和兜底,避免 total=0 却画出巨条
  const scaleOf = function (it: StackItem): number {
    return Math.max(it.total || 0, segSumOf(it))
  }
  const max = Math.max.apply(null, items.map(scaleOf)) || 1
  const out = ['<svg viewBox="0 0 ' + w + ' ' + h + '" class="chart" preserveAspectRatio="xMidYMid meet">']
  items.forEach(function (it, idx) {
    const y = idx * (rowH + gap) + 6
    const scale = scaleOf(it)
    const bw = Math.max(2, scale / max * plotW)
    out.push('<text x="0" y="' + (y + 13) + '" class="lbl">' + esc(clipLabel(it.label, LBL_FS)) + '</text>')
    if (it.sub) out.push('<text x="0" y="' + (y + 28) + '" class="lbl-sub">' + esc(clipLabel(it.sub, SUB_FS)) + '</text>')
    let x = labelW
    const segs = it.segments.filter(function (s) { return s.v > 0 })
    // 「没有分段」才画那根 2 单位灰标；但整行量级为 0 时连标都不画 —— 一个恒为 0
    // 的条配着一根竖线，读起来像「有一点点用量」，实际是没有。
    if (!segs.length && scale > 0) {
      out.push('<rect x="' + labelW + '" y="' + y + '" width="2" height="' + (rowH - 8) + '" fill="var(--dim)" opacity=".4"/>')
    }
    const denom = scale || 1
    segs.forEach(function (s) {
      const sw = (Math.max(0, s.v) / denom) * bw
      out.push('<rect x="' + x.toFixed(2) + '" y="' + y + '" width="' + Math.max(0.8, sw).toFixed(2) +
        '" height="' + (rowH - 8) + '" fill="' + s.color + '"><title>' + esc(s.name) + ': ' + fmt(s.v) +
        ' (' + pct(s.v / denom * 100) + ')</title></rect>')
      x += sw
    })
    out.push('<text x="' + (w - 2) + '" y="' + (y + 18) + '" class="val" text-anchor="end">' + esc(it.totalText) + '</text>')
  })
  out.push('</svg>')
  return out.join('')
}

export function arcPath(cx: number, cy: number, R: number, r: number, a0: number, a1: number): string {
  const large = (a1 - a0) > Math.PI ? 1 : 0
  const x0 = cx + R * Math.cos(a0), y0 = cy + R * Math.sin(a0)
  const x1 = cx + R * Math.cos(a1), y1 = cy + R * Math.sin(a1)
  const x2 = cx + r * Math.cos(a1), y2 = cy + r * Math.sin(a1)
  const x3 = cx + r * Math.cos(a0), y3 = cy + r * Math.sin(a0)
  return 'M' + x0.toFixed(2) + ' ' + y0.toFixed(2) +
    ' A' + R + ' ' + R + ' 0 ' + large + ' 1 ' + x1.toFixed(2) + ' ' + y1.toFixed(2) +
    ' L' + x2.toFixed(2) + ' ' + y2.toFixed(2) +
    ' A' + r + ' ' + r + ' 0 ' + large + ' 0 ' + x3.toFixed(2) + ' ' + y3.toFixed(2) + ' Z'
}

export interface DonutItem { label: string; value: number; color: string }

export function donut(items: DonutItem[], opts: { size?: number } = {}): string {
  const size = opts.size || 230, R = size / 2 - 4, r = R * 0.6, cx = size / 2, cy = size / 2
  const total = items.reduce(function (s, i) { return s + i.value }, 0)
  if (!(total > 0)) {
    return '<div class="empty">没有可用价格，无法计算金额。<br>请在下方「模型明细」里为模型填写单价，或检查 models.dev 价格文件。</div>'
  }
  const out = ['<svg viewBox="0 0 ' + size + ' ' + size + '" class="donut">']
  let a0 = -Math.PI / 2
  items.forEach(function (it) {
    const frac = it.value / total
    if (frac <= 0) return
    if (frac > 0.9999) {
      out.push('<circle cx="' + cx + '" cy="' + cy + '" r="' + ((R + r) / 2).toFixed(2) + '" fill="none" stroke="' +
        it.color + '" stroke-width="' + (R - r).toFixed(2) + '"><title>' + esc(it.label) + ' ' + money(it.value) + '</title></circle>')
      return
    }
    const a1 = a0 + frac * Math.PI * 2
    out.push('<path d="' + arcPath(cx, cy, R, r, a0, a1) + '" fill="' + it.color + '"><title>' +
      esc(it.label) + ': ' + money(it.value) + ' (' + pct(frac * 100) + ')</title></path>')
    a0 = a1
  })
  out.push('<text x="' + cx + '" y="' + (cy - 2) + '" text-anchor="middle" class="donut-num">' + money(total) + '</text>')
  out.push('<text x="' + cx + '" y="' + (cy + 16) + '" text-anchor="middle" class="donut-cap">合计估算</text>')
  out.push('</svg>')
  return out.join('')
}

export interface LegendItem { label: string; color: string; value?: number; valueText?: string }

export function legend(items: LegendItem[]): string {
  return '<div class="legend">' + items.map(function (i) {
    return '<span class="lg"><i style="background:' + i.color + '"></i>' +
      esc(i.label) + (i.value !== undefined ? ' <b>' + esc(i.valueText !== undefined ? i.valueText : fmt(i.value)) + '</b>' : '') + '</span>'
  }).join('') + '</div>'
}

export interface DayRow {
  d: string; total: number; cacheRead: number; cacheWrite: number; input: number; output: number
}

/* X 轴标签抽稀用的字符度量。`.axis` 在 ui.css 里是 10.5px（见 ui.css:35），
 * 这里按等宽数字 0.52em/字估宽，再留 12 单位最小间隙。改了那边字号要回来改这里。 */
const AXIS_CHAR_W = 5.5, AXIS_GAP = 12

export function dayChart(days: DayRow[], costByDay: Record<string, number>): string {
  const w = 940, h = 260, padL = 58, padR = 58, padT = 16, padB = 34
  const plotW = w - padL - padR, plotH = h - padT - padB
  if (!days.length) return '<div class="empty">无数据</div>'
  const maxTok = Math.max.apply(null, days.map(function (d) { return d.total })) || 1
  const maxCost = Math.max.apply(null, days.map(function (d) { return costByDay[d.d] || 0 })) || 1
  const n = days.length, slot = plotW / n, bw = Math.min(46, slot * 0.62)
  const out = ['<svg viewBox="0 0 ' + w + ' ' + h + '" class="chart">']

  /* X 轴标签抽稀：n 天全画会把日期挤成一团糊字（range=all 下 201 天，
   * 旧稿每根柱子后面都跟一个 "10/7"，连成 1/2/2/2/2/2…）。
   * 先按最长标签的实测占宽算出最小步长 step，只保留首、尾与步长网格点；
   * 末位是最重要的锚点，若它与前一个保留点间距不足 step，就把那个点让掉。 */
  const lbls = days.map(function (d) { return dateShort(new Date(d.d + 'T00:00:00').getTime()) })
  const lblChars = Math.max.apply(null, lbls.map(function (t) { return t.length })) || 1
  const lblW = lblChars * AXIS_CHAR_W + AXIS_GAP
  const step = Math.max(1, Math.ceil(lblW / slot))
  const keepAt: boolean[] = new Array(n).fill(false)
  const keep: number[] = []
  for (let i = 0; i < n; i += step) { keepAt[i] = true; keep.push(i) }
  if (keep[keep.length - 1] !== n - 1) {
    // 末位若与前一个保留点间距不足 step，把那个点让掉 —— 末位是最重要的锚点，
    // 两者挤在一起不如只留末位。
    if (n - 1 - keep[keep.length - 1] < step) keepAt[keep[keep.length - 1]] = false
    keepAt[n - 1] = true
  }
  // 首尾标签靠边时改用 start/end 锚点，视觉中心不变，但不会被 viewBox 切掉
  const half = lblW / 2
  const xLabels = lbls.map(function (t, i) {
    if (!keepAt[i]) return ''
    const cx = padL + slot * i + slot / 2
    let anchor = 'middle', tx = cx
    if (i === 0 && cx - half < 2) { anchor = 'start'; tx = Math.max(0, cx - half) }
    else if (i === n - 1 && cx + half > w - 2) { anchor = 'end'; tx = Math.min(w, cx + half) }
    return '<text x="' + tx.toFixed(1) + '" y="' + (h - 12) + '" text-anchor="' + anchor + '" class="axis">' + esc(t) + '</text>'
  })

  // y 轴刻度(token)
  for (let g = 0; g <= 4; g++) {
    const y = padT + plotH - plotH * g / 4
    out.push('<line x1="' + padL + '" x2="' + (w - padR) + '" y1="' + y.toFixed(1) + '" y2="' + y.toFixed(1) + '" class="grid"/>')
    out.push('<text x="' + (padL - 8) + '" y="' + (y + 4).toFixed(1) + '" text-anchor="end" class="axis">' + fmt(maxTok * g / 4) + '</text>')
    out.push('<text x="' + (w - padR + 8) + '" y="' + (y + 4).toFixed(1) + '" class="axis cost-axis">' + money(maxCost * g / 4) + '</text>')
  }

  const line: Array<[number, number, number, string]> = []
  days.forEach(function (d, i) {
    const x = padL + slot * i + (slot - bw) / 2
    let yBase = padT + plotH
    const segs = [
      { v: d.cacheRead, c: CATS[0].color }, { v: d.cacheWrite, c: CATS[1].color },
      { v: d.input, c: CATS[2].color }, { v: d.output, c: CATS[3].color }
    ]
    segs.forEach(function (s) {
      const sh = (s.v / maxTok) * plotH
      if (sh <= 0) return
      yBase -= sh
      out.push('<rect x="' + x.toFixed(1) + '" y="' + yBase.toFixed(1) + '" width="' + bw.toFixed(1) +
        '" height="' + sh.toFixed(1) + '" fill="' + s.c + '"><title>' + d.d + ' ' + fmt(s.v) + '</title></rect>')
    })
    if (xLabels[i]) out.push(xLabels[i])
    const c = costByDay[d.d] || 0
    line.push([padL + slot * i + slot / 2, padT + plotH - (c / maxCost) * plotH, c, d.d])
  })
  out.push('<polyline class="costline" points="' + line.map(function (p) { return p[0].toFixed(1) + ',' + p[1].toFixed(1) }).join(' ') + '"/>')
  line.forEach(function (p) {
    out.push('<circle cx="' + p[0].toFixed(1) + '" cy="' + p[1].toFixed(1) + '" r="3.2" class="costdot"><title>' +
      p[3] + ' 成本 ' + money(p[2]) + '</title></circle>')
  })
  out.push('</svg>')
  return out.join('')
}

/**
 * weekday×hour 活动热力格(7 行 × 24 列)。matrix[w][h]=token;全 0 或空返回降级提示。
 *
 * `sourceLabel` 是这个矩阵的来源自述，**必填且不许传空**：只有 pi 侧有 hours
 * 粒度，外部 CLI 是逐日的，所以这个合计覆盖不到同屏 dayChart 的全工具数。
 * 沿用旧文案「范围内合计 X」会让两个不同口径的数共用一个词 —— §7 铁律 2。
 */
export function heatmap(
  matrix: number[][],
  opts: { caption?: string; emptyText?: string; sourceLabel: string }
): string {
  // 签名要求是一层，运行时空标签也必须挡住：漏传会得到一张没有口径声明的图，
  // 而这正是本项目踩过的坑（数字被当成全量）。宁可不出图。
  if (!opts || !opts.sourceLabel) return ''
  const rows = matrix && matrix.length === 7 ? matrix : null
  let max = 0
  let sum = 0
  let nonzero = 0
  if (rows) {
    rows.forEach(function (r) {
      r.forEach(function (v) {
        if (v > max) max = v
        sum += v
        if (v > 0) nonzero++
      })
    })
  }
  if (!rows || !nonzero || !max) {
    return '<div class="empty">' +
      (opts.sourceLabel ? esc(opts.sourceLabel) + '暂无小时粒度数据' : '暂无小时粒度数据') +
      ' —— 重新跑 tokanary refresh 获取小时粒度后可见热力图。</div>'
  }
  const WAYS = ['日', '一', '二', '三', '四', '五', '六']
  const cell = 28, gap = 3, padL = 36, padT = 22, padB = 8, padR = 8
  const w = padL + 24 * (cell + gap) + padR
  const h = padT + 7 * (cell + gap) + padB
  const out = ['<svg viewBox="0 0 ' + w + ' ' + h + '" class="chart heatmap" preserveAspectRatio="xMidYMid meet">']
  for (let hr = 0; hr < 24; hr += 3) {
    const x = padL + hr * (cell + gap) + cell / 2
    out.push('<text x="' + x.toFixed(1) + '" y="' + (padT - 8) + '" text-anchor="middle" class="axis">' +
      String(hr).padStart(2, '0') + '</text>')
  }
  for (let wd = 0; wd < 7; wd++) {
    const y = padT + wd * (cell + gap)
    out.push('<text x="' + (padL - 8) + '" y="' + (y + cell / 2 + 4) + '" text-anchor="end" class="axis">' +
      WAYS[wd] + '</text>')
    for (let hr = 0; hr < 24; hr++) {
      const v = matrix[wd][hr] || 0
      const x = padL + hr * (cell + gap)
      const op = v > 0 ? (0.18 + 0.82 * Math.sqrt(v / max)) : 0
      const fill = v > 0 ? 'var(--accent)' : 'var(--line)'
      const title = WAYS[wd] + ' ' + String(hr).padStart(2, '0') + ':00 · ' + fmt(v) + ' token'
      out.push('<rect x="' + x + '" y="' + y + '" width="' + cell + '" height="' + cell +
        '" rx="4" fill="' + fill + '" opacity="' + (v > 0 ? op.toFixed(3) : '0.35') +
        '"><title>' + esc(title) + '</title></rect>')
    }
  }
  out.push('</svg>')
  /* 默认 caption 必须自带来源与格数口径。旧的「N 个活跃小时格」把格数说成
   * 「小时」—— 那是 168 格矩阵里的非空格数，不是活跃小时数（§7 铁律 3）。 */
  const cap = opts.caption || (
    (opts.sourceLabel ? opts.sourceLabel + ' · ' : '') +
    '合计 ' + fmt(sum) + ' token · ' + nonzero + ' / ' + (rows.length * 24) + ' 格非零'
  )
  return out + '<div class="hm-cap dim small">' + esc(cap) + '</div>'
}
