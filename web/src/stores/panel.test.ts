// @vitest-environment jsdom
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api, setCSRF } from '../lib/api'
import { queries } from '../lib/query'
import { usePanel } from './panel'
vi.mock('../lib/api', async (original) => ({
  ...(await original<typeof import('../lib/api')>()),
  api: vi.fn(),
  setCSRF: vi.fn(),
}))
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
beforeEach(() => {
  queries.clear()
  vi.resetAllMocks()
  setActivePinia(createPinia())
})
afterEach(() => queries.clear())

it('starts cold datasets in a waiting state, distinguishes failure from a real empty result, and retries', async () => {
  const panel = usePanel()
  expect(panel.pending.resources).toBe(true)
  expect(panel.hasData.resources).toBe(false)
  expect(panel.status.resources).toBe('idle')
  vi.mocked(api).mockRejectedValueOnce(new Error('offline'))
  await panel.refresh(['resources'])
  expect(panel.pending.resources).toBe(false)
  expect(panel.hasData.resources).toBe(false)
  expect(panel.status.resources).toBe('error')
  expect(panel.errors.resources).toBe('offline')
  const result = deferred<never[]>()
  vi.mocked(api).mockReturnValueOnce(result.promise)
  const read = panel.refresh(['resources'])
  expect(panel.pending.resources).toBe(true)
  expect(panel.errors.resources).toBeUndefined()
  result.resolve([])
  await read
  expect(panel.hasData.resources).toBe(true)
  expect(panel.status.resources).toBe('success')
  expect(panel.pending.resources).toBe(false)
})

it('renders valid cached data during refresh failure and clears it at logout', async () => {
  const panel = usePanel()
  vi.mocked(api).mockResolvedValueOnce([])
  await panel.refresh(['resources'])
  vi.mocked(api).mockRejectedValueOnce(new Error('refresh failed'))
  const read = panel.refresh(['resources'])
  expect(panel.pending.resources).toBe(false)
  expect(panel.hasData.resources).toBe(true)
  await read
  expect(panel.hasData.resources).toBe(true)
  expect(panel.errors.resources).toBe('refresh failed')
  panel.clear()
  expect(panel.pending.resources).toBe(true)
  expect(panel.hasData.resources).toBe(false)
  expect(queries.peek('/resources')).toBeUndefined()
})

it('loads datasets independently, merges duplicate reads and ignores results from a cleared session', async () => {
  const panel = usePanel(),
    old = deferred<never[]>()
  vi.mocked(api).mockImplementation((path) =>
    path === '/resources' ? old.promise : Promise.resolve([]),
  )
  const first = panel.refresh(['resources', 'tasks'])
  const second = panel.refresh(['resources'])
  await vi.waitFor(() => expect(panel.hasData.tasks).toBe(true))
  expect(panel.pending.resources).toBe(true)
  expect(vi.mocked(api).mock.calls.filter(([path]) => path === '/resources')).toHaveLength(1)
  panel.clear()
  vi.mocked(api).mockResolvedValue([])
  await panel.refresh(['resources'])
  old.resolve([])
  await Promise.all([first, second])
  expect(panel.hasData.resources).toBe(true)
  expect(panel.hasData.tasks).toBe(false)
  expect(panel.status.resources).toBe('success')
})

it('does not restore authentication or CSRF from a stale session check', async () => {
  const panel = usePanel(),
    old = deferred<any>()
  vi.mocked(api).mockReturnValueOnce(old.promise)
  const read = panel.session()
  panel.clear()
  old.resolve({ authenticated: true, username: 'previous', csrf: 'previous' })
  await read
  expect(panel.auth.authenticated).toBe(false)
  expect(setCSRF).not.toHaveBeenCalledWith('previous')
})
