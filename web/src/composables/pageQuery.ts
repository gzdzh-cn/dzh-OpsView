import {
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
export function usePageQuery<T>(key: MaybeRefOrGetter<string>, initial: T, ttl = 15000) {
  const data = shallowRef<T>(queries.peek<T>(toValue(key)) ?? initial)
  const busy = ref(Boolean(toValue(key)) && queries.peek(toValue(key)) === undefined),
    error = ref('')
  let controller: AbortController | undefined,
    sequence = 0,
    alive = true
  async function load(force = false) {
    const id = ++sequence,
      path = toValue(key)
    if (!path) {
      controller?.abort()
      data.value = initial
      busy.value = false
      error.value = ''
      return
    }
    const cached = queries.peek<T>(path)
    data.value = cached ?? initial
    // Subscribe before releasing a previous consumer of the same in-flight read.
    const previous = controller
    controller = new AbortController()
    busy.value = cached === undefined
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
      if (alive && id === sequence) data.value = result
    } catch (e) {
      if (alive && id === sequence && !isAbort(e)) error.value = errorText(e)
    } finally {
      if (alive && id === sequence) busy.value = false
    }
  }
  watch(
    () => toValue(key),
    () => void load(),
  )
  const refresh = () => {
    if (!document.hidden) void load(ttl < 30000)
  }
  onMounted(() => {
    void load()
    window.addEventListener('opsview:refresh-page', refresh)
  })
  onBeforeUnmount(() => {
    alive = false
    controller?.abort()
    window.removeEventListener('opsview:refresh-page', refresh)
  })
  return { data, busy, error, load }
}
