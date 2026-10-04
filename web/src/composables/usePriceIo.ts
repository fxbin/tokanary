import { ref, type Ref } from 'vue'
import type { UiState } from '../render'

const STORE_KEY = 'pi-token-pricing-v1'

export function usePriceIo(st: UiState) {
  const ioText = ref('')

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

  return { ioText, saveState, loadState, loadCustomPrices, exportPrices, importPrices }
}
