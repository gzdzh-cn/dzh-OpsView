import {
  computed,
  onBeforeUnmount,
  onMounted,
  ref,
  shallowRef,
  watch,
  toValue,
  type MaybeRefOrGetter,
} from 'vue'
import { api, errorText } from '../lib/api'
import { queries, isAbort } from '../lib/query'
import type { ReadStatus } from '../stores/panel'
export function usePageQuery<T>(key: MaybeRefOrGetter<string>, initial: T, ttl = 15000) {
  const data = shallowRef<T>(queries.peek<T>(toValue(key)) ?? initial)
  const hasData = ref(queries.peek(toValue(key)) !== undefined),
    status = ref<ReadStatus>(hasData.value ? 'success' : 'idle'),
    busy = computed(() => Boolean(toValue(key)) && !hasData.value && status.value !== 'error'),
    error = ref('')
  let controller: AbortController | undefined,
    sequence = 0,
    alive = true,
    sessionActive = true
  async function load(force = false) {
    if (!alive || !sessionActive) return
    const id = ++sequence,
      path = toValue(key)
    if (!path) {
      controller?.abort()
      data.value = initial
      hasData.value = false
      status.value = 'idle'
      error.value = ''
      return
    }
    const cached = queries.peek<T>(path)
    data.value = cached ?? initial
    // Subscribe before releasing a previous consumer of the same in-flight read.
    const previous = controller
    controller = new AbortController()
    hasData.value = cached !== undefined
    status.value = 'loading'
    error.value = ''
    const pending = queries.read(
      path,
      (signal) => api<T>(path, { signal }),
      ttl,
      controller.signal,
      force,
    )
    previous?.abort()
    try {
      const result = await pending
      if (alive && id === sequence) {
        data.value = result
        hasData.value = true
        status.value = 'success'
      }
    } catch (e) {
      if (alive && id === sequence) {
        status.value = isAbort(e) ? (hasData.value ? 'success' : 'idle') : 'error'
        if (!isAbort(e)) error.value = errorText(e)
      }
    }
  }
  watch(
    () => toValue(key),
    () => void load(),
    { flush: 'sync' },
  )
  const refresh = () => {
    if (!document.hidden) void load(ttl < 30000)
  }
  function clearSession() {
    sessionActive = false
    sequence++
    controller?.abort()
    data.value = initial
    hasData.value = false
    status.value = 'idle'
    error.value = ''
  }
  window.addEventListener('opsview:session-cleared', clearSession)
  onMounted(() => {
    void load()
    window.addEventListener('opsview:refresh-page', refresh)
  })
  onBeforeUnmount(() => {
    alive = false
    controller?.abort()
    window.removeEventListener('opsview:refresh-page', refresh)
    window.removeEventListener('opsview:session-cleared', clearSession)
  })
  return { data, busy, error, load, hasData, status }
}
