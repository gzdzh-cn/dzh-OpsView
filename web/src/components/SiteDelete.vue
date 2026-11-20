<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import AppDialog from './AppDialog.vue'
import { api, post, errorText, type Config, type Resource } from '../lib/api'
import { isAbort } from '../lib/query'
import { observeSiteTask } from '../lib/siteTask'
const props = defineProps<{ modelValue: boolean; rows: Resource[] }>()
const emit = defineEmits(['update:modelValue', 'completed'])
const visible = computed({
  get: () => props.modelValue,
  set: (value) => emit('update:modelValue', value),
})
const options = ref<Config[]>([]),
  details = ref<Config[]>([]),
  previewId = ref(''),
  error = ref(''),
  previewBusy = ref(false),
  busy = ref(false),
  confirmed = ref(false),
  maintenance = ref(false),
  seconds = ref(5),
  outcomes = ref<Config[]>([])
let generation = 0,
  debounce: ReturnType<typeof setTimeout> | undefined,
  countdown: ReturnType<typeof setInterval> | undefined,
  reader: AbortController | undefined,
  observer: AbortController | undefined
function stop() {
  generation++
  clearTimeout(debounce)
  clearInterval(countdown)
  reader?.abort()
  observer?.abort()
}
onBeforeUnmount(stop)
function schedulePreview() {
  previewId.value = ''
  confirmed.value = false
  seconds.value = 5
  error.value = ''
  clearTimeout(debounce)
  clearInterval(countdown)
  reader?.abort()
  generation++
  if (visible.value && options.value.length) debounce = setTimeout(loadPreview, 180)
}
async function loadPreview() {
  const seq = ++generation
  reader = new AbortController()
  previewBusy.value = true
  try {
    const result = await api('/sites/delete-preview', {
      method: 'POST',
      body: JSON.stringify({ items: options.value }),
      signal: reader.signal,
    })
    if (seq !== generation || !visible.value) return
    details.value = result.items
    let changed = false
    for (const row of result.items) {
      const restored = row.inspection?.recoveryOptions
      const option = options.value.find((v) => v.id === row.id)
      if (restored && option)
        for (const key of ['deleteDirectory', 'deleteDatabase', 'deleteLogs']) {
          if (typeof restored[key] === 'boolean' && option[key] !== restored[key]) {
            option[key] = restored[key]
            changed = true
          }
        }
    }
    if (changed) {
      schedulePreview()
      return
    }

    previewId.value = result.previewId
    seconds.value = result.confirmAfterSeconds
    countdown = setInterval(() => {
      if (document.visibilityState !== 'visible') return
      seconds.value = Math.max(0, seconds.value - 1)
      if (!seconds.value) clearInterval(countdown)
    }, 1000)
  } catch (e) {
    if (!isAbort(e) && seq === generation) error.value = errorText(e)
  } finally {
    if (seq === generation) previewBusy.value = false
  }
}
watch(
  () => props.modelValue,
  (open) => {
    if (!open) {
      stop()
      return
    }
    details.value = []
    outcomes.value = []
    maintenance.value = false
    busy.value = false
    options.value = props.rows.map((r) => ({
      id: r.id,
      revision: r.revision,
      deleteDirectory: false,
      deleteDatabase: false,
      deleteLogs: false,
    }))
    schedulePreview()
  },
)
const needsMaintenance = computed(() => options.value.some((v) => v.deleteDatabase))
async function remove() {
  if (
    busy.value ||
    !previewId.value ||
    seconds.value ||
    !confirmed.value ||
    (needsMaintenance.value && !maintenance.value)
  )
    return
  busy.value = true
  error.value = ''
  observer = new AbortController()
  try {
    const queued = await post('/sites/delete', {
      previewId: previewId.value,
      confirmed: true,
      maintenanceConfirmed: maintenance.value,
    })
    await observeSiteTask(queued.taskId, observer.signal)
    const result = await api('/sites/delete-tasks/' + queued.taskId)
    outcomes.value = result.result?.items || []
    emit('completed')
    if (result.state === 'succeeded') {
      visible.value = false
      return
    }
    error.value = result.error || '删除未完成，请核对逐项结果'
    const failed = new Set(outcomes.value.filter((v) => v.state !== 'succeeded').map((v) => v.id))
    if (outcomes.value.length) options.value = options.value.filter((v) => failed.has(v.id))
    previewId.value = ''
    confirmed.value = false
  } catch (e) {
    if (!isAbort(e)) error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <AppDialog
    v-model="visible"
    title="删除网站"
    width="min(760px,96vw)"
    :close-on-click-modal="false"
    :close-on-press-escape="!busy"
    :show-close="!busy"
  >
    <el-alert
      title="站点配置将删除。未勾选的数据及已有备份保留；勾选数据进入回收站保留 30 天。"
      type="warning"
      :closable="false"
    />
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <div v-if="previewBusy && !details.length" class="site-delete-loading" role="status">
      正在读取关联资源…
    </div>
    <article v-for="row in details" :key="row.id" class="site-delete-item">
      <strong>{{ row.name }}</strong>
      <div class="form-help">{{ row.domains }}</div>
      <template v-for="option in options.filter((v) => v.id === row.id)" :key="option.id">
        <div class="site-delete-choice">
          <el-checkbox
            v-model="option.deleteDirectory"
            :disabled="
              busy ||
              !!row.directoryReason ||
              typeof row.inspection?.recoveryOptions?.deleteDirectory === 'boolean'
            "
            @change="schedulePreview"
          >
            项目目录
          </el-checkbox>
          <span :title="row.directory">{{ row.directoryReason || row.directory }}</span>
        </div>
        <div class="site-delete-choice">
          <el-checkbox
            v-model="option.deleteDatabase"
            :disabled="
              busy ||
              !!row.databaseReason ||
              typeof row.inspection?.recoveryOptions?.deleteDatabase === 'boolean'
            "
            @change="schedulePreview"
          >
            关联数据库
          </el-checkbox>
          <span>{{ row.databaseReason || row.database }}</span>
        </div>
        <div class="site-delete-choice">
          <el-checkbox
            v-model="option.deleteLogs"
            :disabled="busy || typeof row.inspection?.recoveryOptions?.deleteLogs === 'boolean'"
            @change="schedulePreview"
          >
            站点日志
          </el-checkbox>
          <span>移入文件回收站</span>
        </div>
      </template>
      <div v-if="row.services?.length" class="form-help">
        停止并移除：{{ row.services.join('、') }}
      </div>
      <div v-if="row.schedules?.length" class="form-help">
        暂停并保留计划：{{ row.schedules.join('、') }}
      </div>
      <div v-if="row.inspection?.phase !== '未执行'" class="form-help">
        现场状态：{{ row.inspection?.phase }}
      </div>
    </article>
    <el-table v-if="outcomes.length" :data="outcomes" aria-label="删除结果">
      <el-table-column prop="name" label="站点" />
      <el-table-column label="结果">
        <template #default="{ row }">
          {{ row.state === 'succeeded' ? '已删除' : row.error || '未完成' }}
        </template>
      </el-table-column>
    </el-table>
    <el-button v-if="error && !busy" @click="schedulePreview">重新核对删除预览</el-button>
    <template #footer>
      <div class="site-delete-confirm">
        <el-checkbox v-if="needsMaintenance" v-model="maintenance" :disabled="busy">
          已停止相关业务写入
        </el-checkbox>
        <el-checkbox v-model="confirmed" :disabled="busy || previewBusy || !previewId">
          已了解删除影响
        </el-checkbox>
      </div>
      <el-button :disabled="busy" @click="visible = false">取消</el-button>
      <el-button
        type="danger"
        :loading="busy"
        :disabled="
          previewBusy ||
          !previewId ||
          seconds > 0 ||
          !confirmed ||
          (needsMaintenance && !maintenance)
        "
        @click="remove"
      >
        {{ busy ? '删除中' : seconds > 0 ? `确认删除（${seconds}秒）` : '确认删除' }}
      </el-button>
    </template>
  </AppDialog>
</template>
<style scoped>
.site-delete-item {
  padding: 12px 0;
  border-bottom: 1px solid var(--el-border-color-lighter);
  min-width: 0;
}
.site-delete-choice {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.site-delete-choice .el-checkbox {
  flex: 0 0 112px;
}
.site-delete-choice span {
  min-width: 0;
  overflow-wrap: anywhere;
  color: var(--el-text-color-secondary);
}
.site-delete-confirm {
  margin-right: auto;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.site-delete-loading {
  padding: 24px;
  text-align: center;
  color: var(--el-text-color-secondary);
}
@media (max-width: 600px) {
  .site-delete-confirm {
    width: 100%;
  }
}
</style>
