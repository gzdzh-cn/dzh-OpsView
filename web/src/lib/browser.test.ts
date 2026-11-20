// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { randomID, randomBytes, copyText, passkeyUnavailableReason } from './browser'
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})
describe('browser capabilities on public HTTP and HTTPS', () => {
  it('uses native UUID when available', () => {
    const native = vi.fn(() => 'native-uuid')
    vi.stubGlobal('crypto', { randomUUID: native })
    expect(randomID()).toBe('native-uuid')
    expect(native).toHaveBeenCalledOnce()
  })
  it('generates distinct RFC 4122 UUIDs without randomUUID or Math.random', () => {
    let n = 0
    vi.stubGlobal('crypto', {
      getRandomValues: (a: Uint8Array) => {
        a.fill(++n)
        return a
      },
    })
    vi.spyOn(Math, 'random').mockImplementation(() => {
      throw new Error('weak randomness')
    })
    const ids = Array.from({ length: 100 }, randomID)
    expect(new Set(ids).size).toBe(100)
    for (const id of ids)
      expect(id).toMatch(/^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/)
  })
  it('fails explicitly when secure randomness is unavailable', () => {
    vi.stubGlobal('crypto', undefined)
    expect(randomID).toThrow('安全随机数')
    expect(() => randomBytes(16)).toThrow('安全随机数')
  })
  it('copies through the modern clipboard', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    expect(await copyText('secret')).toBe(true)
    expect(writeText).toHaveBeenCalledWith('secret')
  })
  for (const denied of [false, true])
    it(`offers a clean fallback when clipboard ${denied ? 'rejects' : 'is absent'}`, async () => {
      vi.stubGlobal(
        'navigator',
        denied ? { clipboard: { writeText: vi.fn().mockRejectedValue(new Error('denied')) } } : {},
      )
      const exec = vi.fn(() => true)
      Object.defineProperty(document, 'execCommand', { configurable: true, value: exec })
      expect(await copyText('sensitive')).toBe(true)
      expect(exec).toHaveBeenCalledWith('copy')
      expect(document.querySelector('textarea')).toBeNull()
      Object.defineProperty(document, 'execCommand', { configurable: true, value: () => false })
      expect(await copyText('sensitive')).toBe(false)
      expect(document.body.textContent).not.toContain('sensitive')
    })
  it('gates Passkey before invoking browser credentials', () => {
    vi.stubGlobal('isSecureContext', false)
    expect(passkeyUnavailableReason()).toContain('HTTPS')
    vi.stubGlobal('isSecureContext', true)
    vi.stubGlobal('PublicKeyCredential', undefined)
    expect(passkeyUnavailableReason()).toContain('不支持')
    vi.stubGlobal('PublicKeyCredential', function () {})
    vi.stubGlobal('navigator', { credentials: {} })
    expect(passkeyUnavailableReason()).toBe('')
  })
})
