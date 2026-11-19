<script setup lang="ts">
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, post, errorText, date, type Config } from '../lib/api'
import AppDialog from './AppDialog.vue'
import DataList from './DataList.vue'
import StatusBadge from './StatusBadge.vue'
const id = defineModel<string>({ default: '' }),
  rows = ref<Config[]>([]),
  selected = ref<Config>(),
  events = ref<Config[]>([]),
  error = ref(''),
  busy = ref(false),
  state = ref('all'),
  range = ref<[Date, Date]>(),
  loadingLog = ref(false)
const filtered = computed(() =>
  rows.value.filter(
    (r) =>
      (state.value === 'all' || state.value === r.state) &&
      (!range.value ||
        (new Date(r.createdAt) >= range.value[0] && new Date(r.createdAt) <= range.value[1])),
  ),
)
let sequence = 0,
  logSequence = 0,
  stream: EventSource | undefined,
  reader: AbortController | undefined
function closeLog() {
  logSequence++
  stream?.close()
  reader?.abort()
  events.value = []
  selected.value = undefined
}
onBeforeUnmount(() => {
  sequence++
  closeLog()
})
async function load() {
  const seq = ++sequence
  busy.value = true
  error.value = ''
  try {
    const data = await api<Config[]>('/schedules/' + id.value + '/runs')
    if (seq === sequence) rows.value = data
  } catch (e) {
    if (seq === sequence) error.value = errorText(e)
  } finally {
    if (seq === sequence) busy.value = false
  }
}
watch(id, (value) => {
  closeLog()
  rows.value = []
  if (value) void load()
  else sequence++
})
async function show(row: Config) {
  closeLog()
  selected.value = row
  loadingLog.value = true
  const seq = ++logSequence
  reader = new AbortController()
  try {
    const data = await api<Config[]>('/tasks/' + row.id + '/events', { signal: reader.signal })
    if (seq !== logSequence) return
    events.value = data
    stream = new EventSource(
      '/api/v1/tasks/' + row.id + '/events?stream=true&after=' + (data.at(-1)?.sequence || 0),
    )
    stream.onmessage = (e) => {
      if (seq !== logSequence) return
      const data = JSON.parse(e.data)
      if (!events.value.some((v) => v.sequence === data.sequence)) events.value.push(data)
    }
    stream.addEventListener('done', () => {
      stream?.close()
      void load()
    })
    stream.onerror = () => {
      if (seq === logSequence) error.value = '日志连接中断，自动重连中…'
    }
  } catch (e) {
    if (seq === logSequence) error.value = errorText(e)
  } finally {
    if (seq === logSequence) loadingLog.value = false
  }
}
function download() {
  const content = events.value.map((v) => date(v.createdAt) + '  ' + v.message).join('\n')
  const url = URL.createObjectURL(new Blob([content], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = 'opsview-schedule-' + selected.value?.id + '.log'
  a.click()
  URL.revokeObjectURL(url)
}
async function clear() {
  try {
    await ElMessageBox.confirm(
      '清理当前筛选结束时间之前的执行历史和日志？默认清理三十天前，运行中的任务保留。',
      '清理历史',
      { type: 'warning', confirmButtonText: '清理', cancelButtonText: '取消' },
    )
    await post('/schedules/' + id.value + '/clear-history', {
      before: (range.value?.[1] || new Date(Date.now() - 30 * 86400000)).toISOString(),
    })
    closeLog()
    await load()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
</script>
<template>
  <AppDialog
    :model-value="!!id"
    title="计划执行历史"
    width="1000px"
    @update:model-value="id = ''"
    destroy-on-close
  >
    <div class="page-toolbar">
      <el-select v-model="state" aria-label="执行结果">
        <el-option value="all" label="全部结果" />
        <el-option
          v-for="v in [
            'succeeded',
            'failed',
            'cancelled',
            'interrupted',
            'skipped',
            'running',
            'queued',
          ]"
          :key="v"
          :value="v"
          :label="v"
        />
      </el-select>
      <el-date-picker
        v-model="range"
        type="datetimerange"
        start-placeholder="开始时间"
        end-placeholder="结束时间"
      />
    </div>
    <el-alert v-if="error" :title="error" type="warning" :closable="false" />
    <div v-loading="busy">
      <DataList :rows="filtered" state-key="schedule-history">
        <el-table-column label="执行时间" min-width="175">
          <template #default="{ row }">{{ date(row.createdAt) }}</template>
        </el-table-column>
        <el-table-column label="结果" width="110">
          <template #default="{ row }"><StatusBadge :value="row.state" /></template>
        </el-table-column>
        <el-table-column label="退出码 / 原因" min-width="180">
          <template #default="{ row }">
            {{ row.result?.exitCode ?? '—' }} {{ row.error }}
            {{ row.truncated ? '（日志已截断）' : '' }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="90">
          <template #default="{ row }">
            <el-button link type="primary" @click="show(row)">查看日志</el-button>
          </template>
        </el-table-column>
      </DataList>
    </div>
    <div v-if="selected" v-loading="loadingLog">
      <p>{{ date(selected.createdAt) }} · {{ selected.state }}</p>
      <pre class="log-box">{{
        events.map((v) => date(v.createdAt) + '  ' + v.message).join('\n') || '暂无日志'
      }}</pre>
    </div>
    <template #footer>
      <el-button @click="clear">清理历史</el-button>
      <el-button v-if="selected" @click="download">下载当前日志</el-button>
      <el-button @click="id = ''">关闭</el-button>
    </template>
  </AppDialog>
</template>
