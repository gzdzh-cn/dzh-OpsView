import { api, APIError, type Task } from './api'

// Detaching or losing the network never cancels the durable server task.
export function observeSiteTask(id: string, signal: AbortSignal): Promise<Task> {
  return new Promise((resolve, reject) => {
    if (!id) {
      reject(new Error('任务标识缺失，请到任务中心核对创建结果'))
      return
    }
    let stopped = false,
      reading = false,
      failures = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    let request: AbortController | undefined
    let timeout: ReturnType<typeof setTimeout> | undefined
    const stream = new EventSource(`/api/v1/tasks/${encodeURIComponent(id)}/events?stream=true`)
    const cleanup = () => {
      stopped = true
      clearTimeout(timer)
      clearTimeout(timeout)
      request?.abort()
      stream.close()
      signal.removeEventListener('abort', abort)
    }
    const abort = () => {
      cleanup()
      reject(new DOMException('Aborted', 'AbortError'))
    }
    const check = async () => {
      if (stopped || reading) return
      reading = true
      clearTimeout(timer)
      request = new AbortController()
      timeout = setTimeout(() => request?.abort(), 10000)
      try {
        const task = await api<Task>(`/tasks/${encodeURIComponent(id)}`, { signal: request.signal })
        if (stopped) return
        failures = 0
        if (['succeeded', 'failed', 'cancelled', 'interrupted'].includes(task.state)) {
          cleanup()
          resolve(task)
          return
        }
      } catch (error) {
        if (stopped) return
        if (signal.aborted) {
          abort()
          return
        }
        const permanent = error instanceof APIError && [401, 403, 404].includes(error.status)
        if (permanent || ++failures >= 5) {
          cleanup()
          reject(
            new Error(
              permanent
                ? `无法查询任务：${(error as Error).message}；请到任务中心核对`
                : '任务连接暂时不可用，后台任务未取消。请重新打开相关窗口或到任务中心核对',
            ),
          )
          return
        }
      } finally {
        clearTimeout(timeout)
        reading = false
      }
      if (!stopped) timer = setTimeout(check, 1200)
    }
    stream.addEventListener('done', () => {
      void check()
    })
    stream.onerror = () => {
      void check()
    }
    signal.addEventListener('abort', abort, { once: true })
    if (signal.aborted) abort()
    else void check()
  })
}
