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
        <!-- 没有兜底分支：useHashRoute.readHash 只接受 TABS 里列出的 key，
             tab 的类型又是这五个的联合，所以「不在上面任何一个」到不了这里。
             旧稿在这里留了一张写着「当前为占位」的卡片，是一句永远说不出口的实话。 -->
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, onMounted, onUnmounted, nextTick } from 'vue'
import {
  compareSources, defaultOpts, RANGES, POLICIES, freshnessChip, type Policy
} from './pricing'
import {
  renderOverview, renderModels, renderSessions, renderProjects, renderSettings,
  defaultUiState, type UiState
} from './render'
import { useTheme } from './composables/useTheme'
import { useHashRoute, type TabKey } from './composables/useHashRoute'
import { useDashboard } from './composables/useDashboard'
import { usePriceIo } from './composables/usePriceIo'

// note 是这一页自带的说明，写之前先对一遍真实渲染出来的区块名。旧稿这几条
// 是在功能还没做完时写的，改了功能忘了改文案，于是「设置」还写着早已砍掉的
// 套餐限额、总览还指着已经不叫 cost-hero 的读数条。页面上写着不存在的东西，
// 比不写更费劲 —— 用户会去找那个东西。
const TABS = [
  { k: 'overview', label: '总览', note: 'KPI 读数条、花费趋势、热力图与洞察行；跨工具总览随范围联动。' },
  { k: 'models', label: '模型', note: '范围内堆叠条、稳定色 donut 与明细改价表（点表头排序，改价写 localStorage）。' },
  { k: 'sessions', label: '会话', note: '可搜索/排序会话表；burn = 活跃天均摊 token 与费用；recency 相对时间。' },
  { k: 'projects', label: '项目', note: '项目归因条 + 点击钻取（该项目模型构成 / 日趋势 / git 产出 / 会话列表）。' },
  { k: 'settings', label: '设置', note: '预算告警（本月至今比月预算）/ 缺价策略 / 手动改价导入导出；读仓库时顺带在后台补采，跑 tokanary refresh 更快。' }
] as const

const st = reactive<UiState>(defaultUiState())
const { theme, applyTheme, toggleTheme, themeIcon, themeTitle } = useTheme()
const { tab, range, readHash, writeHash, setRange, onHashChange } = useHashRoute(TABS.map((t) => t.k))
const { dataRef, pollFail, loading, loadDashboard, startPolling } = useDashboard()
const { ioText, ioError, saveState, loadState, exportPrices, importPrices } = usePriceIo(st)

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
const tabNote = computed(() => {
  const t = (TABS as readonly { k: string; note: string }[]).find((x) => x.k === tab.value)
  return t ? t.note : ''
})

// 数据新鲜度的时间基准是「采集时刻」，不是「读库时刻」—— 旧稿用 meta.generatedAt
// （dashboard.go 里是 time.Now()，每 30 秒轮询都重写）算相对时间，于是永远显示
// 「刚刚刷新」，而实测 8 个外部工具里有 4 个停更数周。取数、陈旧判定与分级都在
// range.ts 的 freshnessChip，那里可测；这里只做壳绑定。
const fresh = computed(() => freshnessChip(dataRef.value))
const freshText = computed(() => fresh.value.text)
const freshClass = computed(() => fresh.value.cls)
// 新鲜度只在这一处说：chip 上是相对时间，绝对时刻放进它的 title（悬停可见）。
// 旧稿另外在标题下写「更新于 …」、侧栏写「数据 2025-12-27 ~ 2026-10-07」，同一件事说了三遍。
const freshTitle = computed(() => {
  const d = dataRef.value || {}
  const m = d.meta || {}
  const ext = d.external || {}
  // 文案说「采集于」，取值就必须真是采集时刻 —— 旧稿这里写「采集于」却读
  // meta.generatedAt（读库时刻），两个词对不上就是在骗人。
  const stamp = ext.generatedAt || m.generatedAt
  const abs = stamp ? String(stamp).replace('T', ' ').slice(0, 16) : ''
  const range = (m.rangeStart && m.rangeEnd)
    ? '数据区间 ' + String(m.rangeStart).slice(0, 10) + ' ~ ' + String(m.rangeEnd).slice(0, 10) + '。'
    : ''
  const stale = fresh.value.stale
  const nTools = (ext.tools || []).length
  const staleLine = stale.length
    ? (nTools ? '其中 ' : '') + stale.join('、') + ' 的日志超过 7 天没更新，这些工具的费用可能已停更。'
    : ''
  // pi 与外部工具各自停在不同日子，只报一个数字会把两者差掩盖掉
  const piEnd = String(m.rangeEnd || '').slice(0, 10)
  let extEnd = ''
  for (const t of (ext.tools || [])) {
    const day = String((t && t.lastTs) || '').slice(0, 10)
    if (day > extEnd) extEnd = day
  }
  const piLine = (piEnd && extEnd && piEnd < extEnd)
    ? 'pi 侧数据到 ' + piEnd + '，比外部工具旧。'
    : ''
  return (abs ? '采集于 ' + abs + '。' : '') + range + staleLine + piLine +
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
