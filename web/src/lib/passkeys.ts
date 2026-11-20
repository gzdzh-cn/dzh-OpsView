import { passkeyUnavailableReason } from './browser'
import { api, post } from './api'
function decode(s: string) {
  return Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0))
}
function encode(data: ArrayBuffer) {
  return btoa(String.fromCharCode(...new Uint8Array(data)))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '')
}
function serialize(credential: PublicKeyCredential) {
  const r = credential.response as AuthenticatorAttestationResponse & AuthenticatorAssertionResponse
  const response: Record<string, string> = { clientDataJSON: encode(r.clientDataJSON) }
  for (const k of ['attestationObject', 'authenticatorData', 'signature', 'userHandle'] as const)
    if (r[k]) response[k] = encode(r[k])
  return {
    id: credential.id,
    rawId: encode(credential.rawId),
    type: credential.type,
    response,
    authenticatorAttachment: credential.authenticatorAttachment,
    clientExtensionResults: credential.getClientExtensionResults(),
  }
}
export async function registerPasskey(identity: { password: string; code: string }) {
  const reason = passkeyUnavailableReason()
  if (reason) throw new Error(reason)
  const options = await post('/auth/passkeys/register-begin', identity)
  const key = options.publicKey
  key.challenge = decode(key.challenge)
  key.user.id = decode(key.user.id)
  for (const c of key.excludeCredentials || []) c.id = decode(c.id)
  const credential = (await navigator.credentials.create({ publicKey: key })) as PublicKeyCredential
  if (!credential) throw new Error('未创建 Passkey')
  return post('/auth/passkeys/register-finish', serialize(credential))
}
export async function loginPasskey(code: string) {
  const reason = passkeyUnavailableReason()
  if (reason) throw new Error(reason)
  const options = await post('/auth/passkeys/login-begin')
  const key = options.publicKey
  key.challenge = decode(key.challenge)
  for (const c of key.allowCredentials || []) c.id = decode(c.id)
  const credential = (await navigator.credentials.get({ publicKey: key })) as PublicKeyCredential
  if (!credential) throw new Error('未读取 Passkey')
  return api('/auth/passkeys/login-finish', {
    method: 'POST',
    headers: { 'X-OpsView-TOTP': code },
    body: JSON.stringify(serialize(credential)),
  })
}
