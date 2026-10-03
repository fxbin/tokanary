import {
  fmt, money, pct, esc, CATS, POLICIES, SOURCES,
  normalizeCost, costOfTokens, computeAll, compareSources,
  dailyCost, externalSummary, rangeStats, rangeExt, rangeAnchor,
  filterHoursByRange, hourMatrix, rangeInsights, RANGES,
  filterDaysByRange, rangeCutoffKey, calcStreak, weekTopModels,
  type PricingOpts, type PriceSource, type RangeKey
} from '../pricing'
import { MODEL_COLORS, stackBar, donut, legend, dayChart, heatmap, trimNum } from '../charts'

export interface UiState extends PricingOpts {
  customUrl: string
  customFetchedAt: string | null
  customError: string
  sortKey: string
  sortDir: number
  gwQuery: string
  gwSort: string
  sesQuery: string
  sesSortKey: string
  sesSortDir: number
  drillProject: string | null
  budgetUsd: number
}

export function defaultUiState(): UiState {
  return {
    policy: 'ratio10', priceSource: 'modelsdev', overrides: {}, customPrices: null,
    customUrl: '', customFetchedAt: null, customError: '',
    sortKey: 'total', sortDir: -1, gwQuery: '', gwSort: 'inuse',
    sesQuery: '', sesSortKey: 'updatedAt', sesSortDir: -1,
    drillProject: null, budgetUsd: 0
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
    out.push('<span class="tag tag-manual" title="来自「价格设置」里配置的自定义价格源 URL">自定义源</span>')
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

/** 预算告警条:0=关闭;返回空串或 HTML(≥50/80/95 分级)。cost 为比较口径费用(USD)。 */
