import { ref, onUnmounted } from 'vue'

const POLL_MS = 30_000 // 读取时增量刷新，30s 足够接近实时

export function useDashboard() {
  const dataRef = ref<any>(null)
  const pollFail = ref(false)
  const lastError = ref('')
  const loading = ref(false)
  let pollTimer: number | null = null

  async function loadDashboard(enableAI = false) {
    loading.value = true
    try {
      const url = enableAI ? 'api/dashboard?ai=1' : 'api/dashboard'
      const res = await fetch(url, { cache: 'no-store' })
      if (!res.ok) throw new Error('HTTP ' + res.status)
      const json = await res.json()
      if (json && typeof json === 'object') {
        dataRef.value = json
        pollFail.value = false
        lastError.value = ''
      } else {
        // keep previous data rather than blanking the UI
        if (!dataRef.value) pollFail.value = true
        lastError.value = '返回数据为空'
      }
    } catch (err) {
      // 保留上次成功结果，只标失败
      if (!dataRef.value) pollFail.value = true
      lastError.value = String((err as Error)?.message || err)
    } finally {
      loading.value = false
    }
  }

  function startPolling(enableAI = false) {
    void loadDashboard(enableAI)
    pollTimer = window.setInterval(() => { void loadDashboard(enableAI) }, POLL_MS)
  }
  function stopPolling() {
    if (pollTimer !== null) {
      window.clearInterval(pollTimer)
      pollTimer = null
    }
  }
  onUnmounted(stopPolling)

  return { dataRef, pollFail, lastError, loading, loadDashboard, startPolling, stopPolling }
}
