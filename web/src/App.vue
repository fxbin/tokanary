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
            <span v-if="pollFail" class="fresh fresh-err" title="从本地仓库读取数据失败，请先运行 tokanary refresh 重建仓库">读取失败</span>
          </h1>
          <div class="sub" v-html="topSub"></div>
        </div>
        <div class="top-right">
          <div class="seg">
            <button v-for="(r, k) in RANGES" :key="k" :class="{ on: range === k }"
              @click="setRange(k)">{{ r.label }}</button>
          </div>
          <button class="btn-ghost" id="btn-reload-top" @click="loadDashboard" :disabled="loading">
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
import { computed, reactive, onMounted, onUnmounted } from 'vue'
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

const TABS = [
  { k: 'overview', label: '总览', owner: 'U1/U2', note: 'cost-hero、花费趋势、热力图与洞察行已就绪；跨工具总览随范围联动。' },
  { k: 'models', label: '模型', owner: 'U3', note: '范围内堆叠条、稳定色 donut 与明细改价表（点表头排序，改价写 localStorage）。' },
  { k: 'sessions', label: '会话', owner: 'U3', note: '可搜索/排序会话表；burn = 活跃天均摊 token 与费用；recency 相对时间。' },
  { k: 'projects', label: '项目', owner: 'U4', note: '项目归因条 + 点击钻取（该项目模型构成 / 日趋势 / 会话列表）。' },
  { k: 'settings', label: '设置', owner: 'U4', note: '预算告警 / 口径策略 / 自定义源 / 导入导出；页内每 2 分钟静默重读仓库。' }
] as const

const st = reactive<UiState>(defaultUiState())
const { theme, applyTheme, toggleTheme, themeIcon, themeTitle } = useTheme()
const { tab, range, readHash, writeHash, setRange, onHashChange } = useHashRoute(TABS.map((t) => t.k))
const { dataRef, pollFail, loading, loadDashboard, startPolling } = useDashboard()
const { ioText, saveState, loadState, loadCustomPrices, exportPrices, importPrices } = usePriceIo(st)

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

const topSub = computed(() => {
  const m = (dataRef.value || {}).meta
  if (!m) return ''
  const e = (s: unknown) => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;')
  const bits: string[] = []
  if (m.rangeStart || m.rangeEnd) bits.push('区间 ' + e(m.rangeStart || '—') + ' ~ ' + e(m.rangeEnd || '—'))
  if (m.generatedAt) bits.push('生成于 ' + e(m.generatedAt))
  return bits.join(' · ')
})
</script>


<style src="./styles/tokens.css"></style>
<style src="./styles/shell.css"></style>
<style src="./styles/ui.css"></style>
