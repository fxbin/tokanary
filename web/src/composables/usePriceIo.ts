import { ref, type Ref } from 'vue'
import type { UiState } from '../render'

/**
 * 偏好存储键。这个账本覆盖全部检测到的工具，早先的键名却叫
 * `pi-token-pricing-v1` —— 设置页把它当实现细节印出来，看上去就像这个产品
 * 只管 pi。改名会让老用户丢掉手填的价格覆盖与月预算，所以读的时候两个键都认，
 * 写只写新的。
 */
const STORE_KEY = 'tokanary-settings-v1'
const LEGACY_STORE_KEYS = ['pi-token-pricing-v1']

/** 设置页要把它当实现细节印出来（帮助排查「我的设置存哪了」），所以导出。 */
export const STORE_KEY_NAME = STORE_KEY

export function usePriceIo(st: UiState) {
  const ioText = ref('')
  const ioError = ref('')

  function saveState() {
    try {
      localStorage.setItem(STORE_KEY, JSON.stringify({
        policy: st.policy, priceSource: st.priceSource, overrides: st.overrides,
        budgetUsd: st.budgetUsd
      }))
    } catch { /* ignore */ }
  }

  function loadState() {
    try {
      // 新键优先；没有才回落到旧键，这样改名不会静默吃掉用户已填的预算与改价。
      let raw: string | null = null
      try { raw = localStorage.getItem(STORE_KEY) } catch { /* ignore */ }
      if (!raw) {
        for (const k of LEGACY_STORE_KEYS) {
          try { raw = localStorage.getItem(k) } catch { /* ignore */ }
          if (raw) break
        }
      }
      if (raw) {
        const o = JSON.parse(raw)
        if (o && typeof o === 'object') {
          // priceSource 不再持久化：单价只有 models.dev 一个来源
          // (pricing.PriceSource 是单成员联合)，写回只会是死代码。
          st.priceSource = 'modelsdev'
          if (typeof o.policy === 'string') st.policy = o.policy
          if (o.overrides) st.overrides = o.overrides
          if (typeof o.budgetUsd === 'number' && o.budgetUsd >= 0) st.budgetUsd = o.budgetUsd
        }
      }
    } catch { /* 无 localStorage 时忽略 */ }
  }

  /** 导出：只导手动改价覆盖 + 缺价策略（自定义价格源已删，models.dev 底表不入库）。 */
  function exportPrices() {
    const payload = {
      overrides: st.overrides,
      policy: st.policy
    }
    ioError.value = ''
    ioText.value = JSON.stringify(payload, null, 2)
  }

  function importPrices() {
    try {
      const o = JSON.parse(ioText.value || '{}')
      if (!o || typeof o !== 'object') throw new Error('不是对象')
      if (o.overrides && typeof o.overrides === 'object') st.overrides = o.overrides
      st.priceSource = 'modelsdev'
      if (typeof o.policy === 'string') st.policy = o.policy
      saveState()
      ioError.value = ''
    } catch (err) {
      // 导入失败不静默：单列一行红字，且不动用户粘在框里的原文
      ioError.value = '导入失败：' + (err && (err as Error).message ? (err as Error).message : String(err))
    }
  }

  return { ioText, ioError, saveState, loadState, exportPrices, importPrices }
}