import { onBeforeUnmount, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api, post, errorText, type Task } from '../lib/api'

export function useSecurityActions(reload: () => Promise<unknown>, prefix = '/system/security/') {
  const busy = ref(false),
    taskId = ref('')
  const controller = new AbortController()
  onBeforeUnmount(() => controller.abort())
  const delay = () =>
    new Promise<void>((resolve) => {
      const finish = () => {
        clearTimeout(timer)
        controller.signal.removeEventListener('abort', finish)
        resolve()
      }
      const timer = setTimeout(finish, 1000)
      controller.signal.addEventListener('abort', finish, { once: true })
    })
  async function run(action: string, params: unknown = {}, queued = true) {
    if (busy.value) return false
    busy.value = true
    try {
      const result = await post<Task>(prefix + action, params)
      if (queued && result.id) {
        taskId.value = result.id
        for (let n = 0; n < 90 && !controller.signal.aborted; n++) {
          const tasks = await api<Task[]>('/tasks', { signal: controller.signal })
          const task = tasks.find((t) => t.id === result.id)
          if (task && ['failed', 'cancelled', 'interrupted'].includes(task.state))
            throw new Error(task.error || '操作未完成，请检查任务中心')
          if (task?.state === 'succeeded') {
            taskId.value = ''
            break
          }
          await delay()
        }
        if (controller.signal.aborted) return false
        if (taskId.value) {
          ElMessage.info('任务仍在执行，请在任务中心查看')
          return false
        }
      }
      await reload()
      ElMessage.success('操作已完成')
      return true
    } catch (e) {
      if (!controller.signal.aborted) {
        ElMessage.error(errorText(e))
        await reload()
      }
      return false
    } finally {
      busy.value = false
    }
  }
  return { busy, taskId, run }
}
