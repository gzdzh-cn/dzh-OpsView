// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest'
import { api, APIError } from './api'
import { lookupSiteCreation, siteCreationRequest, setSiteCreationRequest } from './siteCreation'
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  api: vi.fn(),
}))
afterEach(() => {
  setSiteCreationRequest('')
  vi.clearAllMocks()
})
it('treats only a genuine 404 as an absent creation request', async () => {
  vi.mocked(api).mockRejectedValueOnce(new APIError('missing', 404))
  expect(await lookupSiteCreation('same-id')).toBeNull()
  for (const error of [
    new APIError('expired', 401),
    new APIError('failed', 500),
    new Error('offline'),
  ]) {
    vi.mocked(api).mockRejectedValueOnce(error)
    await expect(lookupSiteCreation('same-id')).rejects.toBe(error)
  }
})
it('persists only the opaque ID for refresh recovery and clears it on session end', () => {
  setSiteCreationRequest('pending-id')
  expect(siteCreationRequest.value).toBe('pending-id')
  expect(sessionStorage.getItem('opsview:site-creation-request')).toBe('pending-id')
  setSiteCreationRequest('')
  expect(siteCreationRequest.value).toBe('')
  expect(sessionStorage.getItem('opsview:site-creation-request')).toBeNull()
})
