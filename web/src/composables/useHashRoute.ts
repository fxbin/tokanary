import { ref, watch, type Ref } from 'vue'
import type { RangeKey } from '../pricing'

export type TabKey = 'overview' | 'models' | 'sessions' | 'projects' | 'settings'

export function useHashRoute(knownTabs: readonly string[]) {
  const tab = ref<TabKey>('overview')
  // 默认 'all' 而非 '7d':范围锚点是所有源里最新的一天,pi 仓库若落后于外部工具
  // (或反过来),固定 7 天窗口会整页为空,且与「真的没用过」在界面上无法区分。
  // 正解要 Go 侧让 sources 报告真实最新活动日;这里只做止血。
  const range = ref<RangeKey>('all')

  function readHash() {
    const m = (location.hash || '').match(/^#\/(\w+)(?:\?range=(\w+))?/)
    if (m) {
      if (knownTabs.indexOf(m[1]) >= 0) tab.value = m[1] as TabKey
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

  watch(tab, writeHash)

  return { tab, range, readHash, writeHash, setRange, onHashChange }
}
