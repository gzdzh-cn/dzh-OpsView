import { computed, onBeforeUnmount, onMounted, toValue, type MaybeRefOrGetter } from 'vue'
import { useRoute } from 'vue-router'
import { reactive } from 'vue'
// Only navigation/filter primitives belong here. Never store form bodies or credentials.
const values = reactive(new Map<string, string | number | boolean>())
const scroll = new Map<string, number>()
export function clearPageState() {
  values.clear()
  scroll.clear()
}
type Widen<T> = T extends string ? string : T extends number ? number : boolean
export function usePageState<T extends string | number | boolean>(
  key: MaybeRefOrGetter<string>,
  initial: T,
) {
  const route = useRoute(),
    path = route.path
  const id = () => `${path}:${toValue(key)}`
  return computed<Widen<T>>({
    get: () => (values.get(id()) ?? initial) as Widen<T>,
    set: (value) => values.set(id(), value),
  })
}
export function usePageScroll() {
  const path = useRoute().path
  let frame = 0
  onMounted(() => {
    frame = requestAnimationFrame(() => window.scrollTo(0, scroll.get(path) || 0))
  })
  onBeforeUnmount(() => {
    cancelAnimationFrame(frame)
    scroll.set(path, window.scrollY)
  })
}
