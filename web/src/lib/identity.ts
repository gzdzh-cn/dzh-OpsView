import { ElMessageBox } from 'element-plus'
import { usePanel } from '../stores/panel'
export async function identity() {
  const { value: password } = await ElMessageBox.prompt('请输入当前管理员密码', '验证管理员身份', {
    inputType: 'password',
    confirmButtonText: '验证',
    cancelButtonText: '取消',
  })
  let code = ''
  if (usePanel().auth.twoFactorEnabled)
    code = (
      await ElMessageBox.prompt('请输入双因素验证码', '双因素验证', {
        inputPattern: /^\d{6}$/,
        inputErrorMessage: '请输入六位验证码',
        confirmButtonText: '验证',
        cancelButtonText: '取消',
      })
    ).value
  return { password, code }
}
export function saveJSON(value: unknown, name: string) {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(value, null, 2)], { type: 'application/json' }),
  )
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}
