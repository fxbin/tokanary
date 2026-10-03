<template>
  <div>
    <header class="top">
      <h1>Tokanary · AI 用量 &amp; 费用仪表盘
        <span id="fresh" :class="'fresh ' + freshClass" :title="freshTitle">{{ freshText }}</span>
        <span v-if="pollFail" class="fresh fresh-err" title="从本地仓库读取数据失败，请先运行 tokanary refresh 重建仓库">读取失败</span>
        <button id="btn-theme" @click="toggleTheme" :title="themeTitle" v-html="themeIcon"></button>
      </h1>
      <div class="sub" v-html="srcLine"></div>
      <div class="topbar">
        <nav class="tabs">
          <a v-for="t in TABS" :key="t.k" :href="'#/' + t.k + '?range=' + range"
            :class="{ on: tab === t.k }">{{ t.label }}</a>
        </nav>
        <div class="seg">
          <button v-for="(r, k) in RANGES" :key="k" :class="{ on: range === k }"
            @click="setRange(k)">{{ r.label }}</button>
        </div>
      </div>
    </header>
    <div id="app">
      <div v-if="tab === 'overview'" v-html="overviewHtml"></div>
      <div v-else-if="tab === 'models'" v-html="modelsHtml"></div>
      <div v-else-if="tab === 'sessions'" v-html="sessionsHtml"></div>
      <div v-else-if="tab === 'projects'" v-html="projectsHtml"></div>
      <div v-else-if="tab === 'settings'" v-html="settingsHtml"></div>
      <div v-else class="card">
        <h2>{{ tabTitle }} <span class="hint">该视图在 {{ tabOwner }} 建设中，当前为占位</span></h2>
        <p class="dim">{{ tabNote }}</p>
        <p><a href="#/overview?range=7d">← 回总览</a></p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, onMounted, onUnmounted, watch } from 'vue'
import {
  compareSources, defaultOpts, RANGES, POLICIES, type RangeKey, type Policy
} from './pricing'
import {
  renderOverview, renderModels, renderSessions, renderProjects, renderSettings,
  defaultUiState, type UiState
} from './render'

const STORE_KEY = 'pi-token-pricing-v1'
const THEME_KEY = 'tt-theme'
const POLL_MS = 120_000
const TABS = [
  { k: 'overview', label: '总览', owner: 'U1/U2', note: 'cost-hero、花费趋势、热力图与洞察行已就绪；跨工具总览随范围联动。' },
  { k: 'models', label: '模型', owner: 'U3', note: '范围内堆叠条、稳定色 donut 与明细改价表（点表头排序，改价写 localStorage）。' },
  { k: 'sessions', label: '会话', owner: 'U3', note: '可搜索/排序会话表；burn = 活跃天均摊 token 与费用；recency 相对时间。' },
  { k: 'projects', label: '项目', owner: 'U4', note: '项目归因条 + 点击钻取（该项目模型构成 / 日趋势 / 会话列表）。' },
  { k: 'settings', label: '设置', owner: 'U4', note: '预算告警 / 口径策略 / 自定义源 / 网关价目表 / 导入导出已收拢；页内每 2 分钟静默重读仓库。' }
] as const
type TabKey = typeof TABS[number]['k']

const dataRef = ref<any>(null)
const st = reactive<UiState>(defaultUiState())
const tab = ref<TabKey>('overview')
const range = ref<RangeKey>('7d')
const theme = ref<string>('auto')

function saveState() {
  try {
    localStorage.setItem(STORE_KEY, JSON.stringify({
      policy: st.policy, priceSource: st.priceSource, overrides: st.overrides,
      customUrl: st.customUrl, customPrices: st.customPrices,
      customFetchedAt: st.customFetchedAt,
      budgetUsd: st.budgetUsd
    }))
  } catch { /* ignore */ }
}

function loadState() {
  try {
    const raw = localStorage.getItem(STORE_KEY)
    if (raw) {
      const o = JSON.parse(raw)
      if (o && typeof o === 'object') {
        st.priceSource = 'modelsdev'
        if (typeof o.policy === 'string') st.policy = o.policy
        if (o.overrides) st.overrides = o.overrides
        if (typeof o.customUrl === 'string') st.customUrl = o.customUrl.slice(0, 2048)
        if (o.customPrices && typeof o.customPrices === 'object') st.customPrices = o.customPrices
        if (typeof o.customFetchedAt === 'string') st.customFetchedAt = o.customFetchedAt
        if (typeof o.budgetUsd === 'number' && o.budgetUsd >= 0) st.budgetUsd = o.budgetUsd
      }
    }
  } catch { /* 无 localStorage 时忽略 */ }
}

/* ---- hash 路由:#/<tab>?range= ---- */
function readHash() {
  const m = (location.hash || '').match(/^#\/(\w+)(?:\?range=(\w+))?/)
  if (m) {
    const known = (TABS as readonly { k: string }[]).map((t) => t.k)
    if (known.indexOf(m[1]) >= 0) tab.value = m[1] as TabKey
    if (m[2] === 'today' || m[2] === '7d' || m[2] === '30d' || m[2] === 'all') range.value = m[2]
  }
}
function writeHash() {
  const h = '#/' + tab.value + '?range=' + range.value
  if (location.hash !== h) location.hash = h
}
function setRange(r: string) {
  if (r === 'today' || r === '7d' || r === '30d' || r === 'all') {
    range.value = r
    writeHash()
  }
}
function onHashChange() { readHash() }

/* ---- 事件委托:模型排序 / 会话搜索与排序 / 项目钻取 / 设置控件 ---- */
const ioText = ref('')
const pollFail = ref(false)
let pollTimer: number | null = null

function onDocInput(e: Event) {
  const t = e.target as HTMLElement | null
  if (!t) return
  if (t.id === 'ses-search') {
    st.sesQuery = (t as HTMLInputElement).value
    return
  }
  if (t.id === 'gw-search') {
    st.gwQuery = (t as HTMLInputElement).value
    return
  }
  if (t.id === 'custom-url') {
    st.customUrl = (t as HTMLInputElement).value
    return
  }
  if (t.id === 'budget-usd') {
    const v = Number((t as HTMLInputElement).value)
    st.budgetUsd = Number.isFinite(v) && v > 0 ? v : 0
    saveState()
    return
  }
}

function onDocChange(e: Event) {
  const t = e.target as HTMLElement | null
  if (!t) return
  if (t.id === 'policy') {
    const v = (t as HTMLSelectElement).value as Policy
    if (v in POLICIES) st.policy = v
    saveState()
    return
  }
  if (t.id === 'gw-sort') {
    st.gwSort = (t as HTMLSelectElement).value
    return
  }
  if (t instanceof HTMLInputElement && t.name === 'psrc') {
    st.priceSource = 'modelsdev'
  }
}

async function loadCustomPrices() {
  const url = (st.customUrl || '').trim()
  if (!url) {
    st.customError = '请先填写自定义价格源 URL。'
    return
  }
  if (!/^https?:\/\//i.test(url)) {
    st.customError = 'URL 需以 http(s):// 开头。'
    return
  }
  try {
    const res = await fetch(url, { cache: 'no-store' })
    if (!res.ok) throw new Error('HTTP ' + res.status)
    const json = await res.json()
    if (!json || typeof json !== 'object') throw new Error('响应不是 JSON 对象')
    st.customPrices = json
    st.customFetchedAt = new Date().toISOString()
    st.customError = ''
    saveState()
  } catch (err) {
    st.customError = '加载失败：' + (err && (err as Error).message ? (err as Error).message : String(err))
  }
}

function exportPrices() {
  const payload = {
    overrides: st.overrides,
    customPrices: st.customPrices,
    priceSource: st.priceSource,
    policy: st.policy
  }
  ioText.value = JSON.stringify(payload, null, 2)
}

function importPrices() {
  try {
    const o = JSON.parse(ioText.value || '{}')
    if (!o || typeof o !== 'object') throw new Error('不是对象')
    if (o.overrides && typeof o.overrides === 'object') st.overrides = o.overrides
    if (o.customPrices && typeof o.customPrices === 'object') st.customPrices = o.customPrices
    st.priceSource = 'modelsdev'
    if (typeof o.policy === 'string') st.policy = o.policy
    saveState()
    st.customError = ''
  } catch (err) {
    st.customError = '导入失败：' + (err && (err as Error).message ? (err as Error).message : String(err))
  }
}

async function loadDashboard() {
  try {
    const res = await fetch('api/dashboard', { cache: 'no-store' })
    if (!res.ok) throw new Error('HTTP ' + res.status)
    const json = await res.json()
    if (json && typeof json === 'object') {
      dataRef.value = json
      pollFail.value = false
    } else {
      pollFail.value = true
    }
  } catch {
    pollFail.value = true
  }
}

function onDocClick(e: MouseEvent) {
  const target = e.target as HTMLElement | null
  if (!target) return

  const sesSort = target.closest?.('[data-ses-sort]') as HTMLElement | null
  if (sesSort) {
    const k = sesSort.getAttribute('data-ses-sort')
    if (k) {
      if (st.sesSortKey === k) st.sesSortDir = -st.sesSortDir
      else { st.sesSortKey = k; st.sesSortDir = k === 'title' ? 1 : -1 }
    }
    return
  }

  const proj = target.closest?.('[data-proj]') as HTMLElement | null
  if (proj) {
    const name = proj.getAttribute('data-proj')
    if (name) {
      st.drillProject = st.drillProject === name ? null : name
      if (tab.value !== 'projects') {
        tab.value = 'projects'
        writeHash()
      }
    }
    return
  }

  if (target.id === 'drill-back' || target.closest?.('#drill-back')) {
    st.drillProject = null
    return
  }

  if (target.id === 'btn-export' || target.closest?.('#btn-export')) { exportPrices(); return }
  if (target.id === 'btn-import' || target.closest?.('#btn-import')) { importPrices(); return }
  if (target.id === 'btn-reset' || target.closest?.('#btn-reset')) {
    st.overrides = {}
    saveState()
    return
  }
  if (target.id === 'btn-custom-load' || target.closest?.('#btn-custom-load')) {
    void loadCustomPrices()
    return
  }
  if (target.id === 'btn-custom-clear' || target.closest?.('#btn-custom-clear')) {
    st.customUrl = ''
    st.customPrices = null
    st.customFetchedAt = null
    st.customError = ''
    saveState()
    return
  }
  if (target.id === 'btn-reload' || target.closest?.('#btn-reload')) {
    void loadDashboard()
    return
  }
}

onMounted(() => {
  loadState()
  readHash()
  applyTheme()
  void loadDashboard()
  window.addEventListener('hashchange', onHashChange)
  document.addEventListener('input', onDocInput)
  document.addEventListener('change', onDocChange)
  document.addEventListener('click', onDocClick)
  pollTimer = window.setInterval(() => { void loadDashboard() }, POLL_MS)
})
onUnmounted(() => {
  window.removeEventListener('hashchange', onHashChange)
  document.removeEventListener('input', onDocInput)
  document.removeEventListener('change', onDocChange)
  document.removeEventListener('click', onDocClick)
  if (pollTimer !== null) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
})
watch(tab, writeHash)

/* ---- 主题:auto 跟系统,显式值持久化 ---- */
function effectiveTheme(): string {
  if (theme.value === 'light' || theme.value === 'dark') return theme.value
  return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}
function applyTheme() {
  document.documentElement.setAttribute('data-theme', effectiveTheme())
}
function toggleTheme() {
  theme.value = effectiveTheme() === 'dark' ? 'light' : 'dark'
  try { localStorage.setItem(THEME_KEY, theme.value) } catch { /* ignore */ }
  applyTheme()
}
try {
  const saved = localStorage.getItem(THEME_KEY)
  if (saved === 'light' || saved === 'dark') theme.value = saved
} catch { /* ignore */ }
const themeIcon = computed(() => (effectiveThemeReactive() === 'dark'
  ? '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/></svg>'
  : '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>'))
const themeTitle = computed(() => '当前' + (theme.value === 'auto' ? '跟随系统' : theme.value) + '，点击切换明/暗')
// 让图标随 theme 变更刷新
function effectiveThemeReactive(): string {
  void theme.value
  return effectiveTheme()
}

const cmp = computed(() => (dataRef.value ? compareSources(dataRef.value, {
  ...defaultOpts(), policy: st.policy, priceSource: st.priceSource
}) : { gateway: 0, modelsdev: 0 }))
const overviewHtml = computed(() => renderOverview(dataRef.value, st, cmp.value as Record<string, number>, range.value))
const modelsHtml = computed(() => renderModels(dataRef.value, st, cmp.value as Record<string, number>, range.value))
const sessionsHtml = computed(() => renderSessions(dataRef.value, st, cmp.value as Record<string, number>, range.value))
const projectsHtml = computed(() => renderProjects(dataRef.value, st, cmp.value as Record<string, number>, range.value))
const settingsHtml = computed(() => renderSettings(dataRef.value, st, cmp.value as Record<string, number>, range.value, ioText.value))

const tabTitle = computed(() => {
  const t = (TABS as readonly { k: string; label: string }[]).find((x) => x.k === tab.value)
  return t ? t.label : ''
})
const tabOwner = computed(() => {
  const t = (TABS as readonly { k: string; owner: string }[]).find((x) => x.k === tab.value)
  return t ? t.owner : ''
})
const tabNote = computed(() => {
  const t = (TABS as readonly { k: string; note: string }[]).find((x) => x.k === tab.value)
  return t ? t.note : ''
})

/* ---- 标题行:新鲜度徽标 + 数据源行(与旧页同逻辑) ---- */
const fresh = computed(() => {
  const m = (dataRef.value || {}).meta
  if (!m || !m.generatedAt) return { text: '数据时间未知', cls: 'fresh-err' }
  const ts = new Date(m.generatedAt).getTime()
  if (isNaN(ts)) return { text: '数据时间未知', cls: 'fresh-err' }
  const mins = Math.max(0, Math.round((Date.now() - ts) / 60000))
  const text = mins < 1 ? '刚刚刷新' :
    mins < 60 ? mins + ' 分钟前刷新' :
      mins < 1440 ? Math.floor(mins / 60) + ' 小时前刷新' :
        Math.floor(mins / 1440) + ' 天前刷新（数据可能已过期）'
  return { text, cls: mins < 180 ? 'fresh-ok' : mins < 1440 ? 'fresh-warn' : 'fresh-err' }
})
const freshText = computed(() => fresh.value.text)
const freshClass = computed(() => fresh.value.cls)
const freshTitle = '数据来自本地 SQLite 仓库。\n运行 tokanary refresh 采集后，点「重新读取」或等待自动刷新。'
const srcLine = computed(() => {
  const m = (dataRef.value || {}).meta
  if (!m) return ''
  const e = (s: unknown) => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;')
  const parts: string[] = []
  const source = m.sourceNote || m.piDir
  const store = m.dbPath
  if (source && store && source !== store) {
    parts.push('数据源：<code>' + e(source) + '</code> · <code>' + e(store) + '</code>')
  } else {
    parts.push('数据源：<code>' + e(source || store || '—') + '</code>')
  }
  if (m.rangeStart || m.rangeEnd) {
    parts.push('区间 ' + e(m.rangeStart || '—') + ' ~ ' + e(m.rangeEnd || '—'))
  }
  if (m.generatedAt) parts.push('生成于 ' + e(m.generatedAt))
  return parts.join(' · ')
})
</script>

<style>
/* light 分层(s5e3 终裁:结构面中性 + 阅读面暖纸,不混色):
 * 阅读面 --bg/--card/--fg/--dim 走暖纸;结构面 --line 走中性;border/分隔/代码底一律中性。
 * --accent-ink/--ok/--err 按 WCAG AA 校正(切片二光学验收):ink 5.8:1,ok 5.4:1,err 5.1:1。 */
:root, :root[data-theme="light"] {
  --bg: #f7f3eb;
  --card: #fffdf7;
  --fg: #242017;
  --dim: #746c5e;
  --line: #e6e4df;
  --accent: #c98a2e;
  --accent-ink: #8a5a12;
  --accent-2: #e8b45a;
  --clay: #b07d62;
  --cost-line: #6e8b9b;
  --ok: #047857;
  --warn: #b45309;
  --err: #b91c1c;
  --shadow: 0 1px 2px rgba(48, 40, 28, .05), 0 6px 20px rgba(48, 40, 28, .07);
  --glass-blur: 14px;
  --glass-tint: rgba(255, 253, 247, .72);
  --glass-hi: rgba(255, 255, 255, .65);
  --glass-edge: rgba(255, 255, 255, .55);
  /* 中性洗色 token(color-mix 收缩:fg 洗色全部改此三档,字面量随主题) */
  --wash-1: rgba(36, 32, 23, .03);
  --wash-2: rgba(36, 32, 23, .045);
  --wash-3: rgba(36, 32, 23, .07);
  /* accent 洗色/描边 alpha 档(accent 双主题同 hex,声明一次) */
  --accent-a07: rgba(201, 138, 46, .07);
  --accent-a10: rgba(201, 138, 46, .1);
  --accent-a14: rgba(201, 138, 46, .14);
  --accent-a30: rgba(201, 138, 46, .3);
  --accent-a40: rgba(201, 138, 46, .4);
}

@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --bg: #14120f;
    --card: #1c1a16;
    --fg: #efe9df;
    --dim: #a79e8f;
    --line: rgba(201, 138, 46, .18);
    --accent: #c98a2e;
    --accent-ink: #c98a2e;
    --accent-2: #e8b45a;
    --clay: #b07d62;
  --cost-line: #6e8b9b;
    --ok: #34d399;
    --warn: #fbbf24;
    --err: #f87171;
    --shadow: 0 1px 2px rgba(0, 0, 0, .3), 0 8px 24px rgba(0, 0, 0, .35);
    --glass-blur: 14px;
    --glass-tint: rgba(20, 18, 15, .8);
    --glass-hi: rgba(255, 255, 255, .08);
    --glass-edge: rgba(232, 180, 90, .22);
    --wash-1: rgba(239, 233, 223, .04);
    --wash-2: rgba(239, 233, 223, .055);
    --wash-3: rgba(239, 233, 223, .08);
  }
}

:root[data-theme="dark"] {
  --bg: #14120f;
  --card: #1c1a16;
  --fg: #efe9df;
  --dim: #a79e8f;
  --line: rgba(201, 138, 46, .18);
  --accent: #c98a2e;
  --accent-ink: #c98a2e;
  --accent-2: #e8b45a;
  --clay: #b07d62;
  --cost-line: #6e8b9b;
  --ok: #34d399;
  --warn: #fbbf24;
  --err: #f87171;
  --shadow: 0 1px 2px rgba(0, 0, 0, .3), 0 8px 24px rgba(0, 0, 0, .35);
  --glass-blur: 14px;
  --glass-tint: rgba(20, 18, 15, .8);
  --glass-hi: rgba(255, 255, 255, .08);
  --glass-edge: rgba(232, 180, 90, .22);
  --wash-1: rgba(239, 233, 223, .04);
  --wash-2: rgba(239, 233, 223, .055);
  --wash-3: rgba(239, 233, 223, .08);
}

* { box-sizing: border-box; }

body {
  margin: 0;
  padding: 22px clamp(12px, 3vw, 40px) 60px;
  background: var(--bg);
  color: var(--fg);
  font: 15px/1.55 -apple-system, "Segoe UI", "Microsoft YaHei", "PingFang SC", Roboto, Helvetica, Arial, sans-serif;
  -webkit-font-smoothing: antialiased;
}

/* 字体分工(s3e3 终裁,全系统栈 file:// 安全):标题宋体承担分量,正文无衬线,数字 mono tabular */
h1 { font-size: 22px; font-weight: 700; margin: 0 0 4px; letter-spacing: .2px; font-family: "Songti SC", SimSun, "Noto Serif SC", serif; }
h2 { font-size: 16px; font-weight: 700; margin: 0 0 14px; display: flex; align-items: baseline; gap: 10px; flex-wrap: wrap; font-family: "Songti SC", SimSun, "Noto Serif SC", serif; }
.hint { font-size: 12px; font-weight: 400; color: var(--dim); }
code { font-family: ui-monospace, Consolas, "Cascadia Mono", monospace; font-size: .92em; }
.dim { color: var(--dim); }
.small { font-size: 11.5px; }

/* 玻璃落点①顶栏(s4e1):80% 遮罩 + 单层 backdrop-blur + 8% 白内高光(s2e5 stack) + 1px 底界 */
header.top {
  max-width: 1360px; margin: 0 auto 18px;
  position: sticky; top: 0; z-index: 20;
  padding: 8px 0 4px;
  backdrop-filter: blur(var(--glass-blur)) saturate(1.4);
  -webkit-backdrop-filter: blur(var(--glass-blur)) saturate(1.4);
  background: color-mix(in srgb, var(--bg) 80%, transparent);
  box-shadow: inset 0 1px 0 var(--glass-hi);
  border-bottom: 1px solid var(--line);
}
header.top .sub { color: var(--dim); font-size: 12.5px; }
header.top .sub code { background: var(--wash-3); padding: 1px 5px; border-radius: 4px; }
h1 { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }

/* 数据新鲜度徽标 */
.fresh {
  font-size: 11.5px; font-weight: 600; padding: 2px 9px; border-radius: 999px;
  border: 1px solid transparent; white-space: nowrap;
}
.fresh-ok { background: color-mix(in srgb, var(--ok) 14%, transparent); color: var(--ok); border-color: color-mix(in srgb, var(--ok) 32%, transparent); }
.fresh-warn { background: color-mix(in srgb, var(--warn) 16%, transparent); color: var(--warn); border-color: color-mix(in srgb, var(--warn) 34%, transparent); }
.fresh-err { background: color-mix(in srgb, var(--err) 14%, transparent); color: var(--err); border-color: color-mix(in srgb, var(--err) 32%, transparent); }

#btn-theme {
  font-size: 14px; padding: 6px 10px; border-radius: 999px; cursor: pointer;
  border: 1px solid var(--line); background: var(--card); color: var(--fg);
  min-height: 36px;
}
#btn-theme:hover { border-color: var(--accent); }

.topbar { display: flex; align-items: center; gap: 16px; flex-wrap: wrap; margin-top: 12px; }
.tabs { display: flex; gap: 4px; flex-wrap: wrap; }
.tabs a {
  padding: 8px 14px; border-radius: 10px; font-size: 13px; font-weight: 600;
  color: var(--dim); text-decoration: none; border: 1px solid transparent;
  display: inline-flex; align-items: center; min-height: 36px;
}
.tabs a:hover { color: var(--fg); background: var(--wash-2); }
.tabs a.on {
  color: var(--accent-ink); background: var(--accent-a10);
  border-color: var(--accent-a30);
}
.seg {
  display: inline-flex; border: 1px solid var(--line); border-radius: 10px; overflow: hidden;
  background: var(--card);
}
.seg button {
  border: none; border-radius: 0; background: transparent;
  padding: 8px 14px; font-size: 12.5px; font-weight: 600; color: var(--dim); cursor: pointer;
  min-height: 36px;
}
.seg button + button { border-left: 1px solid var(--line); }
.seg button.on { background: var(--accent-a14); color: var(--accent-ink); font-weight: 700; }
.src-quick { display: inline-flex; align-items: center; gap: 6px; font-size: 12.5px; color: var(--dim); }

#app { max-width: 1360px; margin: 0 auto; }

/* KPI: 单行密度读数条(ns-003)——整条一 hairline 面、格间分隔、无阴影;彩顶选择器已退役 */
.kpis {
  display: flex; flex-wrap: wrap; margin-bottom: 16px;
  background: var(--card); border: 1px solid var(--line); border-radius: 12px;
}
.kpi {
  flex: 1 1 180px; min-width: 0; padding: 9px 14px;
  display: flex; align-items: baseline; flex-wrap: wrap; gap: 1px 8px;
}
.kpi + .kpi { border-left: 1px solid var(--line); }
.kpi-l { font-size: 11px; font-weight: 600; color: var(--dim); white-space: nowrap; }
.kpi-v { font-size: 17px; font-weight: 700; letter-spacing: -.3px; font-variant-numeric: tabular-nums; font-family: ui-monospace, Consolas, "Cascadia Mono", monospace; white-space: nowrap; }
.kpi-s { flex-basis: 100%; font-size: 10.5px; color: var(--dim); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
/* 1px 迷你 sparkline(挂在 KPI 条尾,占满整行) */
.spark-row { flex-basis: 100%; padding: 7px 12px; border-top: 1px solid var(--line); }
.spark { display: block; width: 100%; height: 26px; }
.spark polyline { fill: none; stroke: var(--accent-2); stroke-width: 1; vector-effect: non-scaling-stroke; }

.note {
  background: var(--accent-a07);
  border: 1px solid color-mix(in srgb, var(--accent) 25%, var(--line));
  border-radius: 12px; padding: 10px 14px; font-size: 12.5px; color: var(--fg);
  margin-bottom: 16px; line-height: 1.7;
}

.card { background: var(--card); border: 1px solid var(--line); border-radius: 12px; padding: 18px; margin-bottom: 16px; box-shadow: var(--shadow); }
.card a { color: var(--accent-ink); }

.chart { width: 100%; height: auto; display: block; overflow: visible; }
.chart text { font-family: inherit; }
.lbl { font-size: 12.5px; font-weight: 600; fill: var(--fg); }
.lbl-sub { font-size: 10.5px; fill: var(--dim); }
.val { font-size: 12px; font-weight: 600; fill: var(--fg); font-variant-numeric: tabular-nums; }
.axis { font-size: 10.5px; fill: var(--dim); font-variant-numeric: tabular-nums; }
.cost-axis { fill: var(--cost-line); }
.grid { stroke: var(--line); stroke-width: 1; }
.costline { fill: none; stroke: var(--cost-line); stroke-width: 2; stroke-linejoin: round; }
.costdot { fill: var(--cost-line); stroke: var(--card); stroke-width: 1.4; }
.donut { width: 100%; max-width: 240px; height: auto; }
.donut-num { font-size: 19px; font-weight: 700; fill: var(--fg); }
.donut-cap { font-size: 10.5px; fill: var(--dim); }

.legend { display: flex; flex-wrap: wrap; gap: 8px 18px; margin-top: 12px; font-size: 12px; color: var(--dim); }
.lg { display: inline-flex; align-items: center; gap: 6px; }
.lg i { width: 10px; height: 10px; border-radius: 3px; display: inline-block; }
.lg b { color: var(--fg); font-weight: 600; }

.cost-row { display: grid; grid-template-columns: 240px 1fr; gap: 24px; align-items: center; }
@media (max-width: 760px) { .cost-row { grid-template-columns: 1fr; } }
.cost-list { display: flex; flex-direction: column; gap: 7px; }
.cost-li { display: grid; grid-template-columns: 12px minmax(90px, 1.1fr) 2fr 76px 48px 74px; gap: 9px; align-items: center; font-size: 12.5px; }
.cost-li i { width: 10px; height: 10px; border-radius: 3px; }
.cl-name { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cl-bar { background: var(--wash-3); border-radius: 4px; height: 8px; overflow: hidden; }
.cl-bar b { display: block; height: 100%; border-radius: 4px; }
.cl-val { text-align: right; font-variant-numeric: tabular-nums; font-weight: 600; }
.cl-pct, .cl-unit { text-align: right; color: var(--dim); font-variant-numeric: tabular-nums; font-size: 11.5px; }

/* 表格 */
.tbl-wrap { overflow-x: auto; margin: 0 -4px; }
table.tbl { width: 100%; border-collapse: collapse; font-size: 12.5px; }
.tbl th, .tbl td { padding: 7px 9px; border-bottom: 1px solid var(--line); text-align: left; white-space: nowrap; }
.tbl th { font-size: 11.5px; font-weight: 600; color: var(--dim); position: sticky; top: 0; background: var(--card); z-index: 1; }
.tbl th.sortable { cursor: pointer; user-select: none; }
.tbl th.sortable:hover { color: var(--fg); }
.tbl th.on { color: var(--accent-ink); }
.tbl tbody tr:hover { background: var(--wash-1); }
.tbl td.num { text-align: right; font-variant-numeric: tabular-nums; }
.tbl td.strong { font-weight: 700; }
.tbl tr.row-miss { background: color-mix(in srgb, var(--err) 7%, transparent); }
.mname b { font-weight: 600; }
.mname .raw { font-size: 10.5px; color: var(--dim); font-family: ui-monospace, Consolas, monospace; }
.tbl input[type=number] {
  width: 82px; padding: 4px 6px; text-align: right; font: inherit; font-size: 12px;
  color: var(--fg); background: var(--wash-2);
  border: 1px solid var(--line); border-radius: 6px; font-variant-numeric: tabular-nums;
}
.tbl input[type=number]:focus { outline: 2px solid var(--accent-a40); border-color: var(--accent); }
.warn { color: var(--warn); }

.tag { display: inline-block; padding: 1px 6px; border-radius: 999px; font-size: 10.5px; font-weight: 600; border: 1px solid transparent; }
.tag-ok { background: color-mix(in srgb, var(--ok) 14%, transparent); color: var(--ok); border-color: color-mix(in srgb, var(--ok) 30%, transparent); }
.tag-gw { background: color-mix(in srgb, #0ea5e9 15%, transparent); color: #0284c7; border-color: color-mix(in srgb, #0ea5e9 34%, transparent); }
.tag-use { background: var(--accent-a14); color: var(--accent-ink); border-color: var(--accent-a30); }
.tag-est { background: color-mix(in srgb, var(--warn) 16%, transparent); color: var(--warn); border-color: color-mix(in srgb, var(--warn) 32%, transparent); }
.tag-miss { background: color-mix(in srgb, var(--err) 14%, transparent); color: var(--err); border-color: color-mix(in srgb, var(--err) 32%, transparent); }
.tag-manual { background: var(--accent-a14); color: var(--accent-ink); border-color: var(--accent-a30); }

@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) .tag-gw { color: #7dd3fc; }
}
:root[data-theme="dark"] .tag-gw { color: #7dd3fc; }

.gw-bar { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; margin-bottom: 12px; }
.gw-bar input[type=search] {
  font: inherit; font-size: 12.5px; padding: 6px 10px; border-radius: 8px; min-width: 210px;
  border: 1px solid var(--line); background: var(--wash-1); color: var(--fg);
}
.gw-bar input[type=search]:focus { outline: 2px solid var(--accent-a40); border-color: var(--accent); }
.gw-tbl td { font-size: 12px; }
.gw-tbl tr.gw-inuse { background: color-mix(in srgb, var(--ok) 6%, transparent); }
.gw-tbl tr.gw-inuse:hover { background: color-mix(in srgb, var(--ok) 11%, transparent); }
.note-inline { margin-top: 10px; line-height: 1.7; }
.radio { display: inline-flex; align-items: center; gap: 6px; margin-right: 16px; cursor: pointer; }
.radio input { accent-color: var(--accent); }
.radio b { font-variant-numeric: tabular-nums; }

/* 自定义价格源输入框 */
#custom-url {
  font: inherit; font-size: 12.5px; padding: 6px 10px; border-radius: 8px;
  border: 1px solid var(--line); background: var(--wash-1); color: var(--fg);
}
#custom-url:focus { outline: 2px solid var(--accent-a40); border-color: var(--accent); }

/* 跨工具卡片 */
.tool-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); gap: 12px; margin-top: 16px; }
.tool-card {
  border: 1px solid var(--line); border-radius: 12px; padding: 12px 14px;
  background: var(--wash-1);
}
.tc-head { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: 8px; gap: 8px; }
.tc-head b { font-size: 13.5px; }
.tc-cost { font-weight: 700; font-variant-numeric: tabular-nums; color: var(--ok); }
.tc-row { display: flex; justify-content: space-between; font-size: 12px; padding: 2px 0; color: var(--dim); }
.tc-row b { color: var(--fg); font-weight: 600; font-variant-numeric: tabular-nums; }
.tc-foot { margin-top: 7px; font-size: 11px; color: var(--dim); }
.tc-note {
  margin-top: 5px; font-size: 10.5px; color: var(--dim); line-height: 1.5;
  border-top: 1px dashed var(--line); padding-top: 6px;
}

.warn-box {
  margin-top: 14px; padding: 10px 14px; border-radius: 12px; font-size: 12.5px;
  background: color-mix(in srgb, var(--err) 8%, transparent);
  border: 1px solid color-mix(in srgb, var(--err) 28%, transparent);
}

.settings { display: flex; flex-direction: column; gap: 12px; }
.set-row { display: flex; align-items: center; gap: 10px; font-size: 13px; flex-wrap: wrap; }
select {
  font: inherit; font-size: 12.5px; padding: 6px 9px; border-radius: 8px;
  border: 1px solid var(--line); background: var(--card); color: var(--fg); max-width: 100%;
}
.src-info { color: var(--dim); font-size: 12.5px; }
.set-actions { display: flex; gap: 9px; flex-wrap: wrap; }
button {
  font: inherit; font-size: 12.5px; padding: 7px 13px; border-radius: 8px; cursor: pointer;
  border: 1px solid var(--line); background: var(--wash-2); color: var(--fg);
}
button:hover { border-color: var(--accent); color: var(--accent-ink); }
.seg button:hover { border-color: var(--line); color: var(--dim); }
.seg button.on:hover { color: var(--accent-ink); }
textarea {
  width: 100%; min-height: 84px; font-family: ui-monospace, Consolas, monospace; font-size: 11.5px;
  padding: 9px; border-radius: 8px; border: 1px solid var(--line); background: var(--wash-1); color: var(--fg);
}

/* 玻璃落点③空态/占位(s4e1:仅 opacity,禁 backdrop) */
.empty { padding: 26px; text-align: center; color: var(--dim); font-size: 13px; background: var(--wash-1); border: 1px dashed var(--line); border-radius: 12px; }
footer.foot { max-width: 1360px; margin: 8px auto 0; color: var(--dim); font-size: 11.5px; line-height: 1.8; }

/* U2: 洞察行 */
.ins-row { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 12px; }
.ins {
  background: var(--wash-1);
  border: 1px solid var(--line); border-radius: 12px; padding: 12px 14px;
}
.ins-l { font-size: 12px; color: var(--dim); }
.ins-v { font-size: 21px; font-weight: 700; letter-spacing: -.3px; margin: 2px 0; font-variant-numeric: tabular-nums; }
.ins-s { font-size: 11.5px; color: var(--dim); word-break: break-all; }

/* U6: 本周 Top 模型 */
.week-top { display: flex; flex-direction: column; gap: 8px; }
.week-row {
  display: grid; grid-template-columns: 12px minmax(120px, 2fr) 3fr 72px 80px 52px;
  gap: 10px; align-items: center; font-size: 12.5px;
}
.week-dot { width: 10px; height: 10px; border-radius: 3px; }
.week-name { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.week-bar { background: var(--wash-3); border-radius: 4px; height: 8px; overflow: hidden; }
.week-bar b { display: block; height: 100%; border-radius: 4px; }
.week-tok, .week-cost, .week-pct {
  text-align: right; font-variant-numeric: tabular-nums;
}
.week-tok { color: var(--dim); }
.week-cost { font-weight: 600; }
.week-pct { color: var(--dim); font-size: 11.5px; }
@media (max-width: 640px) {
  .week-row { grid-template-columns: 12px minmax(80px, 1.4fr) 1fr 64px 72px; font-size: 12px; }
  .week-pct { display: none; }
}

/* U2: heatmap */
.heatmap rect { transition: opacity .15s; }
.heatmap rect:hover { stroke: var(--fg); stroke-width: 1.5; }
.hm-cap { margin-top: 8px; text-align: right; }

/* U4: 项目钻取 */
.proj-rows { display: flex; flex-direction: column; gap: 6px; margin-top: 14px; }
.proj-row {
  display: grid; grid-template-columns: minmax(80px, 1.4fr) auto auto auto;
  gap: 10px; align-items: center; text-align: left;
  font: inherit; font-size: 12.5px; cursor: pointer;
  border: 1px solid var(--line); border-radius: 10px;
  background: var(--wash-1); color: var(--fg);
  padding: 10px 12px; min-height: 40px;
}
.proj-row:hover { border-color: var(--accent); }
.proj-row.on { border-color: var(--accent); background: var(--accent-a10); }
.proj-row .num { text-align: right; font-variant-numeric: tabular-nums; color: var(--dim); }
.proj-row .strong { text-align: right; font-variant-numeric: tabular-nums; font-weight: 700; }
.drill-back {
  margin-left: auto; font-size: 12px; padding: 6px 12px; border-radius: 8px;
  min-height: 32px;
}

/* U4: 预算分级 */
.budget-ok { border-color: color-mix(in srgb, var(--ok) 40%, var(--line)); }
.budget-warn {
  background: color-mix(in srgb, var(--warn) 10%, var(--card));
  border-color: color-mix(in srgb, var(--warn) 45%, var(--line));
}
.budget-err {
  background: color-mix(in srgb, var(--err) 10%, var(--card));
  border-color: color-mix(in srgb, var(--err) 45%, var(--line));
}
#budget-usd {
  width: 110px; padding: 5px 8px; font: inherit; font-size: 13px;
  color: var(--fg); background: var(--wash-2);
  border: 1px solid var(--line); border-radius: 7px;
  font-variant-numeric: tabular-nums;
}
#budget-usd:focus { outline: 2px solid var(--accent-a40); border-color: var(--accent); }

/* U5: iOS HIG — 窄屏 44px 触控目标 */
@media (max-width: 640px) {
  .tabs a, .seg button, #btn-theme { min-height: 44px; }
  .tabs a { padding: 10px 16px; }
  .seg button { padding: 10px 16px; }
  .proj-row { min-height: 44px; }
  .drill-back { min-height: 44px; padding: 10px 16px; }
}

/* U5: Increase Contrast — 加粗边框、去半透明底 */
@media (prefers-contrast: more) {
  .card, .kpi, .ins, .tool-card, .note, .warn-box {
    border-width: 2px;
    background: var(--card);
  }
  .fresh, .tag { border-width: 1.5px; }
  .tabs a.on, .seg button.on { border-width: 2px; }
  .proj-row { border-width: 2px; }
}

/* U5 + 切片二: Reduce Transparency — 关 backdrop,回落实心(--glass-* 全部落 solid) */
@media (prefers-reduced-transparency: reduce) {
  header.top {
    backdrop-filter: none;
    -webkit-backdrop-filter: none;
    background: var(--bg);
    box-shadow: none;
  }
  .seg {
    backdrop-filter: none;
    -webkit-backdrop-filter: none;
    background: var(--card);
  }
  .empty { background: var(--card); border-color: var(--line); }
  .fresh-ok { background: color-mix(in srgb, var(--ok) 22%, var(--card)); }
  .fresh-warn { background: color-mix(in srgb, var(--warn) 24%, var(--card)); }
  .fresh-err { background: color-mix(in srgb, var(--err) 22%, var(--card)); }
  .tag-ok, .tag-use, .tag-manual { background: color-mix(in srgb, var(--accent) 18%, var(--card)); }
  .tag-gw { background: color-mix(in srgb, #0ea5e9 22%, var(--card)); }
  .tag-est { background: color-mix(in srgb, var(--warn) 22%, var(--card)); }
  .tag-miss { background: color-mix(in srgb, var(--err) 20%, var(--card)); }
  .proj-row, .ins, .tool-card { background: var(--wash-2); }
}</style>
