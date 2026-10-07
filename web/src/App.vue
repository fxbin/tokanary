<template>
  <div class="shell">
    <!-- 侧栏 -->
    <aside class="side">
      <div class="brand">
        <div class="mark">青</div>
        <div class="brand-text">
          <b>Tokanary</b>
          <i>QINGJIAN LEDGER</i>
        </div>
      </div>

      <div class="nav-group">账本</div>
      <a v-for="t in TABS" :key="t.k" :href="'#/' + t.k + '?range=' + range"
        class="nav-item" :class="{ on: tab === t.k }">
        <span class="nav-dot"></span>
        <span class="nav-label">{{ t.label }}</span>
      </a>

      <div class="side-foot">
        <div class="foot-meta" v-html="srcLine"></div>
        <button id="btn-theme" class="btn-theme" @click="toggleTheme" :title="themeTitle" v-html="themeIcon"></button>
      </div>
    </aside>

    <!-- 主区 -->
    <div class="main">
      <header class="top">
        <div class="top-left">
          <h1>{{ tabTitle }}
            <span id="fresh" :class="'fresh ' + freshClass" :title="freshTitle">{{ freshText }}</span>
            <span v-if="pollFail" class="fresh fresh-err">读取失败</span>
          </h1>
        </div>
        <div class="top-right">
          <div class="seg">
            <button v-for="(r, k) in RANGES" :key="k" :class="{ on: range === k }"
              @click="setRange(k)">{{ r.label }}</button>
          </div>
          <button class="btn-ghost" id="btn-reload-top" @click="loadDashboard()" :disabled="loading">
            {{ loading ? '读取中…' : '重新读取' }}
          </button>
        </div>
      </header>

      <div class="body">
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
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, onMounted, onUnmounted, nextTick } from 'vue'
import {
  compareSources, defaultOpts, RANGES, POLICIES, type Policy
} from './pricing'
import {
  renderOverview, renderModels, renderSessions, renderProjects, renderSettings,
  defaultUiState, type UiState
} from './render'
import { useTheme } from './composables/useTheme'
import { useHashRoute, type TabKey } from './composables/useHashRoute'
import { useDashboard } from './composables/useDashboard'
import { usePriceIo } from './composables/usePriceIo'
import { loadQuotas, saveQuotas } from './quota'

const TABS = [
  { k: 'overview', label: '总览', owner: 'U1/U2', note: 'cost-hero、花费趋势、热力图与洞察行已就绪；跨工具总览随范围联动。' },
  { k: 'models', label: '模型', owner: 'U3', note: '范围内堆叠条、稳定色 donut 与明细改价表（点表头排序，改价写 localStorage）。' },
  { k: 'sessions', label: '会话', owner: 'U3', note: '可搜索/排序会话表；burn = 活跃天均摊 token 与费用；recency 相对时间。' },
  { k: 'projects', label: '项目', owner: 'U4', note: '项目归因条 + 点击钻取（该项目模型构成 / 日趋势 / git 产出 / 会话列表）。' },
  { k: 'settings', label: '设置', owner: 'U4', note: '预算告警 / 套餐限额 / 口径策略 / 手动改价导入导出；页内每 30 秒静默重读仓库。' }
] as const

const st = reactive<UiState>(defaultUiState())
const { theme, applyTheme, toggleTheme, themeIcon, themeTitle } = useTheme()
const { tab, range, readHash, writeHash, setRange, onHashChange } = useHashRoute(TABS.map((t) => t.k))
const { dataRef, pollFail, loading, loadDashboard, startPolling } = useDashboard()
const { ioText, ioError, saveState, loadState, exportPrices, importPrices } = usePriceIo(st)

/** 套餐额度读一次进响应式状态，改动即写回 localStorage（总览/设置页共用同一份）。 */
function setQuotaField(planId: string, field: 'limitTokens' | 'limitUsd', raw: string) {
  const v = Number(raw)
  const next = Number.isFinite(v) && v > 0 ? v : 0
  const plans = st.quotas.map((q) => (q.id === planId ? { ...q, [field]: next } : q))
  st.quotas = plans
  saveQuotas(plans)
}

/**
 * 焦点保持：v-html 每次都整体重写 innerHTML，正在编辑的 input 会变成一个全新节点，
 * 焦点直接掉到 body —— 填一次 2000000 要点回输入框 7 次。这里在改 state 之前记下
 * 位置，等 Vue 把 DOM 换完（nextTick）再按 id 找回来。通用机制，所有输入框共用：
 * 会话搜索（边打边过滤，必须留在页面上）、月预算、6 个套餐额度框。
 */
interface FocusSnapshot { id: string; start: number | null; end: number | null }
let pendingFocus: FocusSnapshot | null = null

/** 改 state 之前调用：记下当前焦点输入框的 id 与选区（没有 id 的元素不管）。 */
function captureFocus() {
  const a = document.activeElement as HTMLInputElement | null
  if (!a || !a.id) { pendingFocus = null; return }
  let start: number | null = null
  let end: number | null = null
  // 部分 input type（number 在部分浏览器、email 等）不支持选区，读取会抛。
  try { start = a.selectionStart; end = a.selectionEnd } catch (_e) { /* 无选区概念 */ }
  pendingFocus = { id: a.id, start: start, end: end }
}

/** DOM 更新后把焦点与选区放回同名输入框；不满足条件就放弃，不跟用户抢焦点。 */
function restoreFocus() {
  const snap = pendingFocus
  pendingFocus = null
  if (!snap) return
  const cur = document.activeElement as HTMLElement | null
  // 用户已经在这次更新里把焦点放到别的元素上了（例如点了别的控件），不抢回来。
  if (cur && cur !== document.body && cur.id !== snap.id) return
  const el = document.getElementById(snap.id) as HTMLInputElement | null
  if (!el) return // 视图切换后节点已不在 DOM 里
  el.focus()
  if (snap.start !== null) {
    try { el.setSelectionRange(snap.start, snap.end === null ? snap.start : snap.end) } catch (_e) { /* 不支持选区 */ }
  }
}

function onDocInput(e: Event) {
  const t = e.target as HTMLElement | null
  if (!t) return
  captureFocus()
  applyDocInput(t)
  // nextTick 的回调排在本次组件更新的 flush 之后，等于「DOM 已经换完」。
  nextTick(restoreFocus)
}

/** 事件目标 → 响应式 state。拆出来是为了让 onDocInput 只管焦点。 */
function applyDocInput(t: HTMLElement) {
  if (t.id === 'ses-search') {
    st.sesQuery = (t as HTMLInputElement).value
    return
  }
  const quota = t.id.match(/^quota-(tok|usd)-(.+)$/)
  if (quota) {
    setQuotaField(quota[2], quota[1] === 'tok' ? 'limitTokens' : 'limitUsd', (t as HTMLInputElement).value)
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
  if (target.id === 'btn-reload' || target.closest?.('#btn-reload')) {
    void loadDashboard()
    return
  }
}

onMounted(() => {
  loadState()
  st.quotas = loadQuotas()
  readHash()
  applyTheme()
  startPolling()
  window.addEventListener('hashchange', onHashChange)
  document.addEventListener('input', onDocInput)
  document.addEventListener('change', onDocChange)
  document.addEventListener('click', onDocClick)
})
onUnmounted(() => {
  window.removeEventListener('hashchange', onHashChange)
  document.removeEventListener('input', onDocInput)
  document.removeEventListener('change', onDocChange)
  document.removeEventListener('click', onDocClick)
})

const cmp = computed(() => (dataRef.value ? compareSources(dataRef.value, {
  ...defaultOpts(), policy: st.policy, priceSource: st.priceSource
}) : { gateway: 0, modelsdev: 0 }))
const overviewHtml = computed(() => renderOverview(dataRef.value, st, cmp.value as Record<string, number>, range.value))
const modelsHtml = computed(() => renderModels(dataRef.value, st, cmp.value as Record<string, number>, range.value))
const sessionsHtml = computed(() => renderSessions(dataRef.value, st, cmp.value as Record<string, number>, range.value))
const projectsHtml = computed(() => renderProjects(dataRef.value, st, cmp.value as Record<string, number>, range.value))
const settingsHtml = computed(() => renderSettings(dataRef.value, st, cmp.value as Record<string, number>, range.value, ioText.value, ioError.value))

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
// 新鲜度只在这一处说：chip 上是相对时间，绝对时刻放进它的 title（悬停可见）。
// 旧稿另外在标题下写「更新于 …」、侧栏写「数据 2025-12-27 ~ 2026-10-07」，同一件事说了三遍。
const freshTitle = computed(() => {
  const m = (dataRef.value || {}).meta
  const abs = m && m.generatedAt ? String(m.generatedAt).replace('T', ' ').slice(0, 16) : ''
  const range = (m && m.rangeStart && m.rangeEnd)
    ? '数据区间 ' + String(m.rangeStart).slice(0, 10) + ' ~ ' + String(m.rangeEnd).slice(0, 10) + '。'
    : ''
  return (abs ? '采集于 ' + abs + '。' : '') + range +
    '数据来自本地 SQLite 仓库。运行 tokanary refresh 采集后，点「重新读取」或等待自动刷新。'
})
// 侧栏只留来源名；区间与时间都在顶栏 chip 的 title 里。
const srcLine = computed(() => {
  const m = (dataRef.value || {}).meta
  if (!m) return ''
  const e = (s: unknown) => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;')
  return e(m.sourceNote || m.piDir || '本地仓库')
})
</script>


<style src="./styles/tokens.css"></style>
<style src="./styles/shell.css"></style>
<style src="./styles/ui.css"></style>
