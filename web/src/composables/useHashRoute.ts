import { ref, watch, type Ref } from 'vue'
import type { RangeKey } from '../pricing'

export type TabKey = 'overview' | 'models' | 'sessions' | 'projects' | 'settings'

export function useHashRoute(knownTabs: readonly string[]) {
  const tab = ref<TabKey>('overview')
  const range = ref<RangeKey>('7d')

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
