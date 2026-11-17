// Session-only cache for explicitly selected, non-secret list/status responses.
type Entry = {
  value?: unknown
  updated: number
  generation: number
  pending?: Promise<any>
  controller?: AbortController
  consumers: number
}
export class QueryCache {
  private entries = new Map<string, Entry>()
  peek<T>(key: string): T | undefined {
    return this.entries.get(key)?.value as T | undefined
  }
  invalidate(match: (key: string) => boolean = () => true) {
    for (const [key, entry] of this.entries)
      if (match(key)) {
        entry.updated = 0
        entry.generation++
        // A read started before a mutation must not repopulate the cache.
        entry.controller?.abort()
        entry.pending = undefined
      }
  }
  clear() {
    this.invalidate()
    this.entries.clear()
  }
  async read<T>(
    key: string,
    load: (signal: AbortSignal) => Promise<T>,
    ttl: number,
    signal?: AbortSignal,
    force = false,
  ): Promise<T> {
    if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
    let entry = this.entries.get(key)
    if (!entry) {
      entry = { updated: 0, generation: 0, consumers: 0 }
      this.entries.set(key, entry)
    }
    if (!force && entry.updated && Date.now() - entry.updated < ttl) return entry.value as T
    if (!entry.pending) {
      const generation = entry.generation
      const controller = new AbortController()
      entry.controller = controller
      const current = entry
      const pending = load(controller.signal)
        .then((value) => {
          if (current.generation === generation && !controller.signal.aborted) {
            current.value = value
            current.updated = Date.now()
          }
          if (current.generation !== generation || controller.signal.aborted)
            throw new DOMException('Aborted', 'AbortError')
          return value
        })
        .finally(() => {
          if (current.pending === pending) current.pending = undefined
        })
      entry.pending = pending
    }
    const current = entry,
      pending = entry.pending!,
      controller = entry.controller
    current.consumers++
    return new Promise<T>((resolve, reject) => {
      let done = false
      const finish = (callback: () => void) => {
        if (done) return
        done = true
        signal?.removeEventListener('abort', abort)
        current.consumers--
        callback()
      }
      const abort = () =>
        finish(() => {
          if (current.consumers === 0 && current.pending === pending) {
            controller?.abort()
            current.pending = undefined
          }
          reject(new DOMException('Aborted', 'AbortError'))
        })
      signal?.addEventListener('abort', abort, { once: true })
      pending.then(
        (value) => finish(() => resolve(value)),
        (error) => finish(() => reject(error)),
      )
    })
  }
}
export const queries = new QueryCache()
export const isAbort = (error: unknown) => (error as Error)?.name === 'AbortError'
