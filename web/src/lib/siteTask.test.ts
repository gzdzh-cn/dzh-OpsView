import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api, APIError } from './api'
import { observeSiteTask } from './siteTask'
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  api: vi.fn(),
}))
class Stream {
  static latest: Stream
  close = vi.fn()
  onerror?: () => void
  addEventListener = vi.fn()
  constructor() {
    Stream.latest = this
  }
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.stubGlobal('EventSource', Stream)
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.clearAllMocks()
})
it('survives a read failure and resolves only the actual terminal state', async () => {
  vi.mocked(api)
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValueOnce({ id: 'one', state: 'running' })
    .mockResolvedValueOnce({ id: 'one', state: 'succeeded' })
  const result = observeSiteTask('one', new AbortController().signal)
  await vi.advanceTimersByTimeAsync(2400)
  expect((await result).state).toBe('succeeded')
  expect(Stream.latest.close).toHaveBeenCalledOnce()
  expect(vi.getTimerCount()).toBe(0)
})
it('reports repeated query failure without claiming task failure', async () => {
  vi.mocked(api).mockRejectedValue(new Error('offline'))
  const result = observeSiteTask('one', new AbortController().signal)
  const assertion = expect(result).rejects.toThrow('后台任务未取消')
  await vi.advanceTimersByTimeAsync(6000)
  await assertion
  expect(Stream.latest.close).toHaveBeenCalledOnce()
  expect(vi.getTimerCount()).toBe(0)
})
it('stops immediately for a missing or forbidden task', async () => {
  vi.mocked(api).mockRejectedValue(new APIError('无权访问', 403))
  await expect(observeSiteTask('one', new AbortController().signal)).rejects.toThrow('无权访问')
  expect(api).toHaveBeenCalledOnce()
})
it('aborting the observer closes its stream and read without cancelling the task', async () => {
  const controller = new AbortController()
  let requestSignal: AbortSignal | undefined
  vi.mocked(api).mockImplementation((_path, options) => {
    requestSignal = options?.signal as AbortSignal
    return new Promise(() => {})
  })
  const result = observeSiteTask('one', controller.signal)
  controller.abort()
  await expect(result).rejects.toMatchObject({ name: 'AbortError' })
  expect(requestSignal?.aborted).toBe(true)
  expect(Stream.latest.close).toHaveBeenCalledOnce()
  expect(vi.getTimerCount()).toBe(0)
})
