// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createApp, ref, type App } from 'vue'
import { api } from '../lib/api'
import { queries } from '../lib/query'
import { usePageQuery } from './pageQuery'
vi.mock('../lib/api', async (original) => ({
  ...(await original<typeof import('../lib/api')>()),
  api: vi.fn(),
}))
let app: App | undefined
function mount(key = ref('/items')) {
  let query!: ReturnType<typeof usePageQuery<string[]>>
  app = createApp({
    setup() {
      query = usePageQuery(key, [])
      return () => null
    },
  })
  app.mount(document.createElement('div'))
  return query
}
beforeEach(() => {
  queries.clear()
  vi.resetAllMocks()
})
afterEach(() => {
  app?.unmount()
  queries.clear()
})

it('shows immediate loading, not empty success, and keeps failed reads distinct until retry succeeds', async () => {
  vi.mocked(api).mockRejectedValueOnce(new Error('offline'))
  const query = mount()
  expect(query.busy.value).toBe(true)
  expect(query.hasData.value).toBe(false)
  await vi.waitFor(() => expect(query.status.value).toBe('error'))
  expect(query.busy.value).toBe(false)
  expect(query.hasData.value).toBe(false)
  vi.mocked(api).mockResolvedValueOnce([])
  await query.load(true)
  expect(query.hasData.value).toBe(true)
  expect(query.data.value).toEqual([])
})

it('changes to a cold tab synchronously and rejects late results from the previous tab', async () => {
  let finish!: (value: string[]) => void
  vi.mocked(api).mockImplementation((path) =>
    path === '/one'
      ? new Promise((resolve) => {
          finish = resolve
        })
      : Promise.resolve(['two']),
  )
  const key = ref(''),
    query = mount(key)
  expect(query.busy.value).toBe(false)
  key.value = '/one'
  expect(query.busy.value).toBe(true)
  key.value = '/two'
  await vi.waitFor(() => expect(query.data.value).toEqual(['two']))
  finish(['one'])
  await Promise.resolve()
  expect(query.data.value).toEqual(['two'])
  key.value = ''
  expect(query.busy.value).toBe(false)
  expect(query.hasData.value).toBe(false)
})

it('retains even empty cached results while a background read fails', async () => {
  await queries.read('/items', async () => [], 1000)
  vi.mocked(api).mockRejectedValue(new Error('offline'))
  const query = mount()
  expect(query.busy.value).toBe(false)
  expect(query.hasData.value).toBe(true)
  await query.load(true)
  expect(query.error.value).toBe('offline')
  expect(query.hasData.value).toBe(true)
  expect(query.data.value).toEqual([])
})

it('stops readers before session filter reset and never subscribes again from the disposed session', async () => {
  let finish!: (value: string[]) => void
  vi.mocked(api).mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      }),
  )
  const key = ref('/items'),
    query = mount(key)
  window.dispatchEvent(new Event('opsview:session-cleared'))
  queries.clear()
  key.value = '/reset-by-logout'
  window.dispatchEvent(new Event('opsview:refresh-page'))
  finish(['previous session'])
  await Promise.resolve()
  await Promise.resolve()
  expect(api).toHaveBeenCalledTimes(1)
  expect(query.hasData.value).toBe(false)
  expect(query.data.value).toEqual([])
})
