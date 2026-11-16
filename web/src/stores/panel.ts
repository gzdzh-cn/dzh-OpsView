import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api, setCSRF, type Resource, type Task } from '../lib/api'
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
  const loaded = ref(false)
  async function session() {
    const data = await api<any>('/auth')
    setCSRF(data.csrf)
    auth.value = data
    loaded.value = true
    return data
  }
  async function refresh() {
    const results = await Promise.all([
      api<Resource[]>('/resources'),
      api<Task[]>('/tasks'),
      api<any[]>('/catalog'),
      api('/overview'),
    ])
    ;[resources.value, tasks.value, catalog.value, overview.value] = results
  }
  function kind(name: string) {
    return resources.value.filter((r) => r.kind === name)
  }
  return { auth, resources, tasks, catalog, overview, loaded, session, refresh, kind }
})
