import { describe, it, expect, vi } from 'vitest'
import { QueryCache } from './query'
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => {
    resolve = r
  })
  return { promise, resolve }
}
describe('session query cache', () => {
  it('deduplicates reads, honors TTL, and preserves successful data on refresh failure', async () => {
    const cache = new QueryCache(),
      pending = deferred<number>(),
      load = vi.fn(() => pending.promise)
    const one = cache.read('items', load, 300000),
      two = cache.read('items', load, 300000)
    expect(load).toHaveBeenCalledTimes(1)
    pending.resolve(7)
    expect(await one).toBe(7)
    expect(await two).toBe(7)
    expect(await cache.read('items', load, 300000)).toBe(7)
    expect(load).toHaveBeenCalledTimes(1)
    await expect(
      cache.read('items', () => Promise.reject(new Error('offline')), 0, undefined, true),
    ).rejects.toThrow('offline')
    expect(cache.peek('items')).toBe(7)
  })
  it('does not allow a pre-mutation response to overwrite a new response', async () => {
    const cache = new QueryCache(),
      old = deferred<string>()
    const previous = cache.read('items', () => old.promise, 1000)
    cache.invalidate()
    await cache.read('items', async () => 'new', 1000)
    old.resolve('old')
    await expect(previous).rejects.toMatchObject({ name: 'AbortError' })
    expect(cache.peek('items')).toBe('new')
  })
  it('cancels only abandoned reads and keeps other consumers alive', async () => {
    const cache = new QueryCache(),
      pending = deferred<number>(),
      one = new AbortController(),
      two = new AbortController()
    let transport!: AbortSignal
    const load = (s: AbortSignal) => {
      transport = s
      return pending.promise
    }
    const a = cache.read('items', load, 0, one.signal),
      b = cache.read('items', load, 0, two.signal)
    one.abort()
    await expect(a).rejects.toMatchObject({ name: 'AbortError' })
    expect(transport.aborted).toBe(false)
    two.abort()
    await expect(b).rejects.toMatchObject({ name: 'AbortError' })
    expect(transport.aborted).toBe(true)
    pending.resolve(1)
    await Promise.resolve()
    expect(cache.peek('items')).toBeUndefined()
  })
  it('clears session data and rejects cache repopulation by old reads', async () => {
    const cache = new QueryCache(),
      pending = deferred<number>()
    const a = cache.read('items', () => pending.promise, 1000)
    cache.clear()
    pending.resolve(3)
    await expect(a).rejects.toMatchObject({ name: 'AbortError' })
    expect(cache.peek('items')).toBeUndefined()
  })
})

it('revalidates expired entries while retaining the old value for immediate rendering', async () => {
  const now = vi.spyOn(Date, 'now').mockReturnValue(1000)
  try {
    const cache = new QueryCache(),
      load = vi.fn(async () => 1)
    await cache.read('catalog', load, 300000)
    now.mockReturnValue(300999)
    await cache.read('catalog', load, 300000)
    expect(load).toHaveBeenCalledTimes(1)
    now.mockReturnValue(301001)
    const delayed = deferred<number>()
    const request = cache.read('catalog', () => delayed.promise, 300000)
    expect(cache.peek('catalog')).toBe(1)
    delayed.resolve(2)
    expect(await request).toBe(2)
  } finally {
    now.mockRestore()
  }
})
