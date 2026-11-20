// Feature detection is required: public HTTP and localhost expose different APIs.
export function randomID(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID()
  const bytes = randomBytes(16)
  bytes[6] = (bytes[6]! & 0x0f) | 0x40
  bytes[8] = (bytes[8]! & 0x3f) | 0x80
  const hex = Array.from(bytes, (v) => v.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}
export function randomBytes(length: number): Uint8Array {
  if (typeof globalThis.crypto?.getRandomValues !== 'function')
    throw new Error('当前浏览器无法生成安全随机数，请更换浏览器后重试')
  return globalThis.crypto.getRandomValues(new Uint8Array(length))
}
export function passkeyUnavailableReason(): string {
  if (!globalThis.isSecureContext)
    return 'Passkey 需要通过 HTTPS 安全地址访问，当前仍可使用密码登录'
  if (typeof globalThis.PublicKeyCredential === 'undefined' || !globalThis.navigator?.credentials)
    return '当前浏览器不支持 Passkey，请使用密码登录'
  return ''
}
export async function copyText(value: string): Promise<boolean> {
  try {
    if (typeof navigator.clipboard?.writeText === 'function') {
      await navigator.clipboard.writeText(value)
      return true
    }
  } catch {
    /* A denied clipboard permission must still allow manual copying. */
  }
  // Keep this synchronous fallback inside the user's click activation on HTTP.
  const active = document.activeElement as HTMLElement | null
  const field = document.createElement('textarea')
  field.value = value
  field.readOnly = true
  field.setAttribute('aria-label', '复制内容')
  field.style.cssText = 'position:fixed;left:-9999px;top:0;opacity:0'
  // Inside the active dialog so modal focus trapping does not steal the selection.
  const parent = active?.closest('[role="dialog"]') || document.body
  parent.appendChild(field)
  try {
    field.select()
    return typeof document.execCommand === 'function' && document.execCommand('copy')
  } catch {
    return false
  } finally {
    field.value = ''
    field.remove()
    active?.focus({ preventScroll: true })
  }
}
