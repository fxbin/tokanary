import {
  fmt, money, pct, esc as escHtml, CATS, POLICIES, SOURCES,
  normalizeCost, costOfTokens, computeAll, compareSources,
  dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor,
  filterHoursByRange, hourMatrix, rangeInsights, RANGES,
  filterDaysByRange, rangeCutoffKey, calcStreak, weekTopModels,
  type PricingOpts, type PriceSource, type RangeKey
} from '../pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, heatmap, trimNum } from '../charts'
import type { QuotaPlan } from '../quota'

/**
 * esc():HTML 转义之前先洗掉 U+FFFD 替换字符。
 * git commit subject 从仓库原样读出,遇到坏字节(常见于在多字节字符中间被截断的
 * subject)终端渲染成 �,HTML 里就会把 U+FFFD 直接印到界面上。整条剔除,不留问号。
 * 注意:pricing.ts 里那个 esc 不做这一步,渲染层一律用本文件导出的这个。
 */
export function esc(s: unknown): string {
  const t = String(s === null || s === undefined ? '' : s)
  return escHtml(t.indexOf('�') >= 0 ? t.replace(/�/g, '') : t)
}

export interface UiState extends PricingOpts {
  sortKey: string
  sortDir: number
  sesQuery: string
  sesSortKey: string
  sesSortDir: number
  drillProject: string | null
  budgetUsd: number
  /** 套餐额度；由 App.vue 从 localStorage 读入，改动即写回（saveQuotas）。 */
  quotas: QuotaPlan[]
}

export function defaultUiState(): UiState {
  return {
    policy: 'ratio10', priceSource: 'modelsdev', overrides: {}, customPrices: null,
    sortKey: 'total', sortDir: -1,
    sesQuery: '', sesSortKey: 'updatedAt', sesSortDir: -1,
    drillProject: null, budgetUsd: 0,
    quotas: []
  }
}

export function th(sortKey: string, sortDir: number, key: string, label: string): string {
  const arrow = sortKey === key ? (sortDir < 0 ? ' ▾' : ' ▴') : ''
  return '<th class="sortable' + (sortKey === key ? ' on' : '') + '" data-sort="' + key + '">' + esc(label) + arrow + '</th>'
}

export function sourceTag(key: string, p: any, st: UiState): string {
  if (p.source === 'missing') return '<span class="tag tag-miss">未匹配</span>'
  const meta = p.meta || {}
  const ov = st.overrides[key] || {}
  const out: string[] = []
  if (p.source === 'manual') {
    out.push('<span class="tag tag-manual">' + esc(ov.label || '手动填价') + '</span>')
  } else if (p.source === 'custom') {
    // 单价只走 models.dev(pricing.ts 保留该分支,本应用已不再加载自定义源):
    // 标签仍按「非 models.dev」标出来,免得把未知来源当成 models.dev 报价。
    out.push('<span class="tag tag-manual" title="来自调用方传入的自定义单价(本应用设置页已不再提供自定义价格源)">自定义源</span>')
  } else {
    out.push('<span class="tag tag-ok">models.dev</span>')
  }
  if (p.fellBack) {
    out.push('<span class="tag tag-est" title="所选口径没有该模型的报价，已回退到' +
      'models.dev' + '">回退</span>')
  }
  if (p.estimated) {
    out.push('<span class="tag tag-est" title="缺这些字段，已按当前缺价策略推算：' +
      esc(p.estimatedFields.join(', ')) + '">推算</span>')
  }
  if (meta.confidence === 'medium') out.push('<span class="tag tag-est" title="近似匹配，非完全同名条目">近似</span>')
  const prov = meta.provider || ''
  if (prov) out.push('<span class="dim small">' + esc(prov) + '</span>')
  if (meta.note) out.push('<span class="info" title="' + esc(meta.note) + '">备注</span>')

  const vs = meta.variants || []
  if (vs.length > 1) {
    out.push('<div class="var-sel"><select data-variant="' + esc(key) + '" title="切换计费渠道">' +
      '<option value=""' + (ov.variant === undefined ? ' selected' : '') + '>按当前口径</option>' +
      vs.map(function (v: any, i: number) {
        return '<option value="' + i + '"' + (ov.variant === i ? ' selected' : '') + '>' + esc(v.label) + '</option>'
      }).join('') + '</select></div>')
  }
  return out.join(' ')
}

/** 整页主体(不含标题行,标题行由 App.vue 模板直写) */

/* ------------------------------------------------------------------ U3: 模型 / 会话 Tab */

/** 模型 key → 稳定色(按字典序索引,跨重渲染不变色) */
export function stableColorIndex(keys: string[]): Record<string, number> {
  const sorted = keys.slice().sort()
  const map: Record<string, number> = {}
  sorted.forEach(function (k, i) { map[k] = i })
  return map
}

export function sesRecency(ts: number | null | undefined): string {
  if (!ts) return '—'
  const diff = Date.now() - ts
  if (diff < 0) return '刚刚'
  const m = Math.floor(diff / 60000)
  if (m < 1) return '刚刚'
  if (m < 60) return m + ' 分钟前'
  const h = Math.floor(m / 60)
  if (h < 24) return h + ' 小时前'
  const d = Math.floor(h / 24)
  if (d < 30) return d + ' 天前'
  return new Date(ts).toLocaleDateString('zh-CN')
}


/* ------------------------------------------------------------------ U4: 项目 / 设置 Tab */

export function pad2(x: number): string { return String(x).padStart(2, '0') }
export function localDay(ts: number): string {
  const dt = new Date(ts)
  return dt.getFullYear() + '-' + pad2(dt.getMonth() + 1) + '-' + pad2(dt.getDate())
}

export interface EmptyState {
  /** 当前 tab,用于「切到全部」的 hash 链接 */
  tab: string
  rangeLabel: string
  /** 名词,如「会话」「项目」 */
  what: string
  /** 未做任何过滤时的条数:0 = 仓库里从来没有过 */
  allCount: number
  /** 未过滤时最新的一条落在哪一天 */
  lastDay: string | null
}

/**
 * 空态必须能区分两种成因:
 *  - allCount === 0 → 真的从来没数据 → 让用户去采集;
 *  - allCount  > 0 → 数据在窗口外(切到更小的范围就会出现) → 给「切到全部」的出口。
 * 旧文案两种情况都写「换更大时间范围试试」,于是用户面对真没数据时也在被指去改范围。
 */
export function emptyStateHtml(e: EmptyState): string {
  if (!e.allCount) {
    // allCount 是「不做任何过滤」的条数：0 说明仓库里压根没有这类记录，
    // 这时让用户去改时间范围是错的，该让他去采集。
    return '<div class="empty"><b>当前范围没有' + esc(e.what) + '</b>：' +
      '仓库里也没有任何记录 —— 先跑 <code>tokanary collect</code> 采集，再点右上角「重新读取」。</div>'
  }
  return '<div class="empty"><b>' + esc(e.rangeLabel) + '窗口内没有' + esc(e.what) + '</b>' +
    '：全量共 ' + e.allCount + ' 条，最近一条在 ' + esc(e.lastDay || '未知日期') + '，落在窗口外。<br>' +
    '<a href="#/' + esc(e.tab) + '?range=all">切到「全部」</a>看全量，或点右上角「重新读取」重新采集。</div>'
}

/** 预算告警条:0=关闭;返回空串或 HTML(≥50/80/95 分级)。cost 为比较口径费用(USD)。 */
