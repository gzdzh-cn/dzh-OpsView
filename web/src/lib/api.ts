import { queries } from './query'
import type { components } from './api.generated'
export type Resource = Omit<components['schemas']['Resource'], 'config'> & { config: Config }
export type Task = components['schemas']['Task']
export type Config = Record<string, any>
let csrf = ''
export function getCSRF(){return csrf}
let activity=Math.floor(Date.now()/1000)
if(typeof window!=='undefined')for(const event of ['pointerdown','keydown','touchstart'])window.addEventListener(event,()=>{activity=Math.floor(Date.now()/1000)},{passive:true})
export class APIError extends Error {
  constructor(
    message: string,
    public status: number,
    public code?: string,
    public revision?: string,
  ) {
    super(message)
  }
}
export function setCSRF(token: string) {
  csrf = token
}
export async function api<T = any>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch('/api/v1' + path, {
    ...options,
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'X-OpsView-Activity': String(activity), ...options.headers },
  })
  const data = await response.json().catch(() => ({ error: `HTTP ${response.status}` }))
  if (!response.ok) {
    if (response.status === 401) window.dispatchEvent(new Event('opsview:unauthorized'))
    throw new APIError(data.error || '请求失败', response.status, data.code, data.revision)
  }
  if (
    options.method &&
    !['GET', 'HEAD'].includes(options.method.toUpperCase()) &&
    !path.startsWith('/auth') &&
    !path.startsWith('/files') &&
    !path.startsWith('/redis') &&
    !path.endsWith('/credential')
  ) {
    queries.invalidate(
      (key) =>
        (path.startsWith('/databases') && key.startsWith('/databases')) ||
        key === '/resources' ||
        (path.startsWith('/resources') && key === '/schedules') ||
        key === '/tasks' ||
        key === '/overview' ||
        (path.startsWith('/resources')
          ? key.startsWith('/sites/') && key.endsWith('/traffic')
          : false) ||
        (path.startsWith('/docker') || path.includes('docker')
          ? key.startsWith('/docker')
          : false) ||
        (path.startsWith('/plugins') ||
        path.startsWith('/resources') ||
        path.startsWith('/repositories')
          ? ['/catalog', '/plugins'].includes(key)
          : false) ||
        (path.startsWith('/system') ? key.startsWith('/system') || key === '/audit' : false) ||
        (path.startsWith('/settings') ? key.startsWith('/settings') : false) ||
        (path.startsWith('/schedules') ? key.startsWith('/schedules') : false) ||
        (path.startsWith('/scripts') ? key.startsWith('/scripts') : false) ||
        (path.startsWith('/notifications') ? key.startsWith('/notifications') : false) ||
        (path.startsWith('/backups') ? key.startsWith('/backups') : false),
    )
  }
  return data as T
}
export async function downloadPOST(path: string, body: unknown, filename: string) {
  const response = await fetch('/api/v1' + path, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
    body: JSON.stringify(body),
  })
  if (!response.ok) throw new Error('下载失败，请检查实例和键状态')
  const url = URL.createObjectURL(await response.blob())
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}
export const post = <T = any>(path: string, body: unknown = {}) =>
  api<T>(path, { method: 'POST', body: JSON.stringify(body) })
export const put = <T = any>(path: string, body: unknown) =>
  api<T>(path, { method: 'PUT', body: JSON.stringify(body) })
export const action = (id: string, operation: string, params: unknown = {}) =>
  post<Task>(`/resources/${id}/actions/${operation}`, params)
export function errorText(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}
export function size(bytes: number = 0) {
  if (!bytes) return '0 B'
  const i = Math.min(4, Math.floor(Math.log(bytes) / Math.log(1024)))
  return `${(bytes / 1024 ** i).toFixed(i > 0 ? 1 : 0)} ${['B', 'KB', 'MB', 'GB', 'TB'][i]}`
}
export function date(value?: string) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
}
export function toBase64(text: string) {
  const bytes = new TextEncoder().encode(text)
  let s = ''
  for (const v of bytes) s += String.fromCharCode(v)
  return btoa(s)
}
export function fromBase64(text: string) {
  return new TextDecoder().decode(Uint8Array.from(atob(text), (c) => c.charCodeAt(0)))
}
