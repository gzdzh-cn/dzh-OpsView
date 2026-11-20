import { h, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { copyText } from './browser'
export async function copyOrSelect(value: string) {
  if (await copyText(value)) {
    ElMessage.success('已复制')
    return
  }
  const content = ref(value)
  value = ''
  let closeManual = () => {}
  const clear = () => {
    content.value = ''
    closeManual()
  }
  window.addEventListener('opsview:session-cleared', clear, { once: true })
  const timeout = setTimeout(clear, 30000)
  try {
    await ElMessageBox.alert(
      ({ close }) => {
        closeManual = close
        return h('div', [
          h('p', '浏览器未允许自动复制，请选中下方内容手动复制。内容将在 30 秒后清除。'),
          h('textarea', {
            value: content.value,
            readonly: true,
            rows: 4,
            class: 'el-textarea__inner',
            'aria-label': '手动复制内容',
            onFocus: (event: FocusEvent) => (event.target as HTMLTextAreaElement).select(),
          }),
        ])
      },
      '手动复制',
      { confirmButtonText: '关闭' },
    )
  } catch {
    /* Closing the manual-copy dialog is not an operation failure. */
  } finally {
    clearTimeout(timeout)
    content.value = ''
    window.removeEventListener('opsview:session-cleared', clear)
  }
}
