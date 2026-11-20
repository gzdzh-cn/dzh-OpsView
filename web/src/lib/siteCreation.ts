import { ref } from 'vue'
import { api, APIError, type Config } from './api'
const key = 'opsview:site-creation-request'
function readPending() {
  try {
    return sessionStorage.getItem(key) || ''
  } catch {
    return ''
  }
}
// Only an opaque request ID is stored. Never persist a form or its credentials.
export const siteCreationRequest = ref(readPending())
export function setSiteCreationRequest(value: string) {
  siteCreationRequest.value = value
  try {
    if (value) sessionStorage.setItem(key, value)
    else sessionStorage.removeItem(key)
  } catch {
    /* In-memory recovery remains available when storage is disabled. */
  }
}
export async function lookupSiteCreation(id: string, signal?: AbortSignal): Promise<Config | null> {
  try {
    return await api('/sites/provisions/' + encodeURIComponent(id), { signal })
  } catch (e) {
    if (e instanceof APIError && e.status === 404) return null
    throw e
  }
}
