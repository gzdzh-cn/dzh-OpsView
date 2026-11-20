import { api, type Task } from './api'

// Observes a durable task; closing the view only detaches this observer.
export function observeSiteTask(id: string, signal: AbortSignal): Promise<Task> {
  return new Promise((resolve, reject) => {
    let stopped = false,
      reading = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const stream = new EventSource(`/api/v1/tasks/${encodeURIComponent(id)}/events?stream=true`)
    const cleanup = () => {
      stopped = true
      clearTimeout(timer)
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
      try {
        const task = await api<Task>(`/tasks/${id}`, { signal })
        if (stopped) return
        if (['succeeded', 'failed', 'cancelled', 'interrupted'].includes(task.state)) {
          cleanup()
          resolve(task)
          return
        }
      } catch (error) {
        if (signal.aborted) {
          abort()
          return
        }
        // A transient read/stream failure does not change the server task state.
      } finally {
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
