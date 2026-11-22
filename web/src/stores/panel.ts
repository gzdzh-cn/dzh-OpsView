import { defineStore } from 'pinia'
import { computed, ref, reactive } from 'vue'
import { api, setCSRF, errorText, type Resource, type Task } from '../lib/api'
import { queries, isAbort } from '../lib/query'
import { clearPageState } from '../composables/pageState'
import { setSiteCreationRequest } from '../lib/siteCreation'
import { cachedDirectorySizes } from '../lib/fileSizes'
export type Dataset = 'resources' | 'tasks' | 'catalog' | 'overview'
export type ReadStatus = 'idle' | 'loading' | 'success' | 'error'
export const usePanel = defineStore('panel', () => {
  const auth = ref({
    setupRequired: false,
    authenticated: false,
    username: '',
    twoFactorEnabled: false,
    development: false,
    temporary: false,
    passwordExpired: false,
    scopes: [] as string[],
  })
  const resources = ref<Resource[]>([]),
    tasks = ref<Task[]>([]),
    catalog = ref<any[]>([]),
    overview = ref<any>(null)
  const loaded = ref(false),
    errors = reactive<Partial<Record<Dataset, string>>>({}),
    status = reactive<Record<Dataset, ReadStatus>>({
      resources: 'idle',
      tasks: 'idle',
      catalog: 'idle',
      overview: 'idle',
    }),
    hasData = reactive<Record<Dataset, boolean>>({
      resources: false,
      tasks: false,
      catalog: false,
      overview: false,
    })
  // An unstarted read is still awaiting data. Never render an empty list as a result.
  const pending = computed(
    () =>
      Object.fromEntries(
        (Object.keys(status) as Dataset[]).map((key) => [
          key,
          !hasData[key] && (status[key] === 'idle' || status[key] === 'loading'),
        ]),
      ) as Record<Dataset, boolean>,
  )
  const datasets = { resources, tasks, catalog, overview }
  const readers: Partial<Record<Dataset, AbortController>> = {}
  const epoch = ref(0)
  let sessionSequence = 0
  const counts: Partial<Record<Dataset, number>> = {}
  function retain(keys: Dataset[]) {
    for (const key of Object.keys(readers) as Dataset[])
      if (!keys.includes(key)) {
        readers[key]?.abort()
        delete readers[key]
        counts[key] = (counts[key] || 0) + 1
        status[key] = hasData[key] ? 'success' : 'idle'
      }
  }
  function clear() {
    retain([])
    epoch.value++
    sessionSequence++
    // Stop page readers before clearing reactive filters can trigger fresh reads.
    setCSRF('')
    window.dispatchEvent(new Event('opsview:session-cleared'))
    queries.clear()
    clearPageState()
    setSiteCreationRequest('')
    cachedDirectorySizes.clear()
    resources.value = []
    tasks.value = []
    catalog.value = []
    overview.value = null
    for (const key of Object.keys(datasets) as Dataset[]) {
      delete errors[key]
      status[key] = 'idle'
      hasData[key] = false
      delete counts[key]
    }
  }
  async function session() {
    const id = ++sessionSequence,
      currentEpoch = epoch.value
    const data = await api<any>('/auth')
    if (id !== sessionSequence || currentEpoch !== epoch.value) return auth.value
    if (!data.authenticated || (auth.value.username && auth.value.username !== data.username))
      clear()
    setCSRF(data.csrf)
    auth.value = data
    loaded.value = true
    return data
  }
  async function refresh(keys: Dataset[] = ['resources', 'tasks'], force = true) {
    const currentEpoch = epoch.value
    await Promise.allSettled(
      keys.map(async (key) => {
        const id = (counts[key] = (counts[key] || 0) + 1)
        const cached = queries.peek<any>('/' + key)
        if (cached !== undefined) {
          datasets[key].value = cached
          hasData[key] = true
        }
        status[key] = 'loading'
        delete errors[key]
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
          if (currentEpoch === epoch.value && counts[key] === id) {
            datasets[key].value = value
            hasData[key] = true
            status[key] = 'success'
            delete errors[key]
          }
        } catch (e) {
          if (currentEpoch === epoch.value && counts[key] === id) {
            status[key] = isAbort(e) ? (hasData[key] ? 'success' : 'idle') : 'error'
            if (!isAbort(e)) errors[key] = errorText(e)
          }
        } finally {
          if (currentEpoch === epoch.value && counts[key] === id) delete readers[key]
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
    sessionVersion: epoch,
    errors,
    pending,
    status,
    hasData,
    session,
    refresh,
    kind,
    clear,
    retain,
  }
})
