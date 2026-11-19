<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { api, post, errorText } from '../lib/api'
import { usePanel } from '../stores/panel'
import DataList from './DataList.vue'
const panel = usePanel(),
  view = ref('overview'),
  data = ref<any>(),
  busy = ref(true),
  error = ref(''),
  allowed = computed(() => panel.auth.scopes || [])
const labels: Record<string, string> = { overview: '概览', resources: '资源', tasks: '任务' }
const reads = new AbortController()
onBeforeUnmount(() => {
  reads.abort()
  data.value = undefined
})
async function load() {
  busy.value = true
  data.value = undefined
  error.value = ''
  try {
    data.value = await api('/' + view.value, { signal: reads.signal })
  } catch (e) {
    if (!reads.signal.aborted) error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
onMounted(() => {
  view.value =
    ['overview', 'resources', 'tasks'].find((k) => allowed.value.includes(k + ':read')) ||
    'overview'
  void load()
})
async function leave() {
  await post('/auth/logout')
  await panel.session()
}
</script>
<template>
  <main class="main-content" aria-label="临时只读访问">
    <div class="page-toolbar">
      <el-tag>临时只读访问</el-tag>
      <el-select v-model="view" @change="load">
        <el-option
          v-for="(label, key) in labels"
          :key="key"
          :label="label"
          :value="key"
          :disabled="!allowed.includes(key + ':read')"
        />
      </el-select>
      <el-button @click="leave">退出访问</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <div v-loading="busy" class="surface">
      <DataList v-if="Array.isArray(data)" :rows="data" state-key="temporary">
        <el-table-column prop="name" label="名称" />
        <el-table-column prop="kind" label="类型" />
        <el-table-column prop="status" label="状态" />
        <el-table-column prop="operation" label="操作" />
        <el-table-column prop="state" label="结果" />
      </DataList>
      <el-descriptions v-else-if="data" :column="1" border>
        <el-descriptions-item label="服务器">{{ data.host?.hostname }}</el-descriptions-item>
        <el-descriptions-item label="平台">{{ data.host?.platform }}</el-descriptions-item>
        <el-descriptions-item label="内存">{{ data.memory?.usedPercent }}%</el-descriptions-item>
      </el-descriptions>
    </div>
  </main>
</template>
