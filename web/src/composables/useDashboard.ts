import { ref, onUnmounted } from 'vue'

const POLL_MS = 30_000 // 读取时增量刷新，30s 足够接近实时

export function useDashboard() {
  const dataRef = ref<any>(null)
  const pollFail = ref(false)
  const loading = ref(false)
  let pollTimer: number | null = null

  async function loadDashboard() {
    loading.value = true
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
    } finally {
      loading.value = false
    }
  }

  function startPolling() {
    void loadDashboard()
    pollTimer = window.setInterval(() => { void loadDashboard() }, POLL_MS)
  }
  function stopPolling() {
    if (pollTimer !== null) {
      window.clearInterval(pollTimer)
      pollTimer = null
    }
  }
  onUnmounted(stopPolling)

  return { dataRef, pollFail, loading, loadDashboard, startPolling, stopPolling }
}
