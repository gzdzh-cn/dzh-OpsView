import { describe, expect, it } from 'vitest'
import { size, fromBase64, toBase64 } from './api'
describe('file transport and display', () => {
  it('preserves Chinese and emoji through the file API', () => {
    const text = '部署完成 🟢\nserver { listen 80; }'
    expect(fromBase64(toBase64(text))).toBe(text)
  })
  it('formats actual byte counters', () => {
    expect(size(0)).toBe('0 B')
    expect(size(1024)).toBe('1.0 KB')
    expect(size(1073741824)).toBe('1.0 GB')
  })
})

it('discards a late response belonging to a previous authenticated session', async () => {
  const { api, setCSRF } = await import('./api')
  const { vi } = await import('vitest')
  let complete!: (response: Response) => void
  vi.stubGlobal(
    'fetch',
    vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          complete = resolve
        }),
    ),
  )
  setCSRF('old-session')
  const result = api('/resources')
  setCSRF('new-session')
  complete(new Response(JSON.stringify({ error: 'expired' }), { status: 401 }))
  await expect(result).rejects.toMatchObject({ name: 'AbortError' })
  setCSRF('')
  vi.unstubAllGlobals()
})
