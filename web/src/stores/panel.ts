import { defineStore } from 'pinia'
import { ref, reactive } from 'vue'
import { api, setCSRF, errorText, type Resource, type Task } from '../lib/api'
import { queries, isAbort } from '../lib/query'
import { clearPageState } from '../composables/pageState'
import { cachedDirectorySizes } from '../lib/fileSizes'
export type Dataset = 'resources' | 'tasks' | 'catalog' | 'overview'
export const usePanel = defineStore('panel', () => {
  const auth = ref({
    setupRequired: false,
    authenticated: false,
    username: '',
    twoFactorEnabled: false,
    development: false,
  })
  const resources = ref<Resource[]>([]),
    tasks = ref<Task[]>([]),
    catalog = ref<any[]>([]),
    overview = ref<any>(null)
  const loaded = ref(false),
    errors = reactive<Partial<Record<Dataset, string>>>({}),
    pending = reactive<Partial<Record<Dataset, boolean>>>({})
  const datasets = { resources, tasks, catalog, overview }
  const readers: Partial<Record<Dataset, AbortController>> = {}
  let epoch = 0
  const counts: Partial<Record<Dataset, number>> = {}
  function retain(keys: Dataset[]) {
    for (const key of Object.keys(readers) as Dataset[])
      if (!keys.includes(key)) {
        readers[key]?.abort()
        delete readers[key]
        counts[key] = (counts[key] || 0) + 1
        pending[key] = false
      }
  }
  function clear() {
    retain([])
    epoch++
    queries.clear()
    clearPageState()
    cachedDirectorySizes.clear()
    setCSRF('')
    resources.value = []
    tasks.value = []
    catalog.value = []
    overview.value = null
    for (const key of Object.keys(datasets) as Dataset[]) {
      delete errors[key]
      delete pending[key]
      delete counts[key]
    }
  }
  async function session() {
    const data = await api<any>('/auth')
    if (!data.authenticated || (auth.value.username && auth.value.username !== data.username))
      clear()
    setCSRF(data.csrf)
    auth.value = data
    loaded.value = true
    return data
  }
  async function refresh(keys: Dataset[] = ['resources', 'tasks'], force = true) {
    const currentEpoch = epoch
    await Promise.allSettled(
      keys.map(async (key) => {
        const id = (counts[key] = (counts[key] || 0) + 1)
        pending[key] = queries.peek('/' + key) === undefined
        const previous = readers[key]
        const reader = (readers[key] = new AbortController())
        try {
          const request = queries.read(
            '/' + key,
            (signal) => api('/' + key, { signal }),
            key === 'catalog' ? 300000 : 15000,
            reader.signal,
            force,
          )
          previous?.abort()
          const value = await request
          if (currentEpoch === epoch && counts[key] === id) {
            datasets[key].value = value
            delete errors[key]
          }
        } catch (e) {
          if (currentEpoch === epoch && counts[key] === id && !isAbort(e))
            errors[key] = errorText(e)
        } finally {
          if (currentEpoch === epoch && counts[key] === id) pending[key] = false
        }
      }),
    )
  }
  function kind(name: string) {
    return resources.value.filter((r) => r.kind === name)
  }
  return {
    auth,
    resources,
    tasks,
    catalog,
    overview,
    loaded,
    errors,
    pending,
    session,
    refresh,
    kind,
    clear,
    retain,
  }
})
