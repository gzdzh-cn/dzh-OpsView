<script setup lang="ts">
import LoadingContent from '../components/LoadingContent.vue'
import AppDialog from '../components/AppDialog.vue'
import AppDrawer from '../components/AppDrawer.vue'
import { isAbort } from '../lib/query'
import { usePageState } from '../composables/pageState'
import DataList from '../components/DataList.vue'
import { ElMessage } from 'element-plus'
import { computed, onUnmounted, ref } from 'vue'
import { Clock, ArrowRight } from '@element-plus/icons-vue'
import { usePanel } from '../stores/panel'
import { post, api, date, errorText, type Task } from '../lib/api'
import StatusBadge from '../components/StatusBadge.vue'
const panel = usePanel(),
  filter = usePageState('filter', 'all'),
  selected = ref<Task>(),
  events = ref<any[]>([]),
  error = ref('')
let stream: EventSource | undefined,
  reader: AbortController | undefined,
  sequence = 0
const fileResult = ref<any>(),
  resultOpen = ref(false)
async function viewResult(id: string) {
  try {
    fileResult.value = await api(`/files/tasks/${id}`)
    resultOpen.value = true
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
const tasks = computed(() =>
  panel.tasks.filter((t) => filter.value === 'all' || t.state === filter.value),
)
async function open(task: Task) {
  const id = ++sequence
  reader?.abort()
  reader = new AbortController()
  stream?.close()
  selected.value = task
  events.value = []
  error.value = ''
  try {
    const result = await api(`/tasks/${task.id}/events`, { signal: reader.signal })
    if (id !== sequence) return
    events.value = result
    {
      const after = events.value.at(-1)?.sequence || 0
      stream = new EventSource(`/api/v1/tasks/${task.id}/events?stream=true&after=${after}`)
      stream.onopen = () => {
        if (id !== sequence) return
        error.value = ''
      }
      stream.onmessage = (e) => {
        if (id !== sequence) return
        try {
          const event = JSON.parse(e.data)
          if (!events.value.some((v) => v.sequence === event.sequence)) events.value.push(event)
        } catch {
          error.value = '日志数据格式异常，请重新打开任务核对'
        }
      }
      stream.addEventListener('done', () => {
        if (id !== sequence) return
        stream?.close()
        void panel.refresh()
      })
      stream.onerror = () => {
        if (id !== sequence) return
        if (!error.value) error.value = '日志连接暂时中断，正在自动重连…'
      }
    }
  } catch (e) {
    if (!isAbort(e) && id === sequence) error.value = errorText(e)
  }
}
function close() {
  sequence++
  reader?.abort()
  stream?.close()
  selected.value = undefined
}
onUnmounted(close)
async function cancelInstall(id: string) {
  try {
    await post(`/tasks/${id}/cancel`)
    await panel.refresh()
    ElMessage.success('已请求取消任务')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
</script>
<template>
  <div class="resource-shortcuts" style="margin-top: 0; margin-bottom: 20px">
    <article
      v-for="state in [
        { key: 'running', label: '执行中' },
        { key: 'queued', label: '等待中' },
        { key: 'succeeded', label: '已完成' },
        { key: 'failed', label: '失败' },
      ]"
      :key="state.key"
      class="surface resource-shortcut"
    >
      <span class="quick-icon" :class="state.key === 'failed' ? 'amber' : 'green'"><Clock /></span>
      <div>
        <span>{{ state.label }}</span>
        <strong>
          {{ panel.tasks.filter((t) => t.state === state.key).length }}
          <small>项</small>
        </strong>
      </div>
    </article>
  </div>
  <section class="surface">
    <div class="page-toolbar">
      <h3>任务记录</h3>
      <el-select v-model="filter" style="width: 140px" size="small">
        <el-option label="全部状态" value="all" />
        <el-option
          v-for="s in ['queued', 'running', 'succeeded', 'failed', 'interrupted', 'cancelled']"
          :key="s"
          :label="
            {
              queued: '等待中',
              running: '执行中',
              succeeded: '已完成',
              failed: '失败',
              interrupted: '已中断',
              cancelled: '已取消',
            }[s]
          "
          :value="s"
        />
      </el-select>
    </div>
    <LoadingContent :busy="panel.pending.tasks">
      <DataList
        state-key="Tasks-1"
        v-if="tasks.length"
        :rows="tasks"
        @row-click="open"
        style="cursor: pointer"
      >
        <el-table-column label="操作" min-width="200">
          <template #default="{ row }">
            <span class="task-operation">{{ row.operation }}</span>
            <small class="cell-subtext">{{ row.id }}</small>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }"><StatusBadge :value="row.state" /></template>
        </el-table-column>
        <el-table-column label="提交时间" min-width="160">
          <template #default="{ row }">
            <span class="cell-date">{{ date(row.createdAt) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="结果" min-width="180">
          <template #default="{ row }">
            <span v-if="row.error" style="color: var(--el-color-danger)">{{ row.error }}</span>
            <span v-else class="muted">
              {{ row.state === 'succeeded' ? '操作已完成' : '等待执行结果' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="120">
          <template #default="{ row }">
            <el-button
              v-if="
                (row.operation === 'instance.install' || row.operation.startsWith('files.')) &&
                ['queued', 'running'].includes(row.state)
              "
              link
              type="danger"
              @click.stop="cancelInstall(row.id)"
            >
              取消任务
            </el-button>
            <el-button
              v-if="row.operation.startsWith('files.') && row.state === 'succeeded'"
              link
              type="primary"
              @click.stop="viewResult(row.id)"
            >
              查看结果
            </el-button>
          </template>
        </el-table-column>
        <el-table-column width="60">
          <template #default>
            <el-icon><ArrowRight /></el-icon>
          </template>
        </el-table-column>
      </DataList>
      <div v-else class="empty-page">
        <span class="empty-symbol"><Clock /></span>
        <h3>暂无任务记录</h3>
        <p>安装软件、部署网站和执行备份后，这里将显示真实进度与日志。</p>
      </div>
    </LoadingContent>
  </section>
  <AppDialog v-model="resultOpen" title="文件任务结果" width="min(760px, 95vw)">
    <pre class="log-box">{{ JSON.stringify(fileResult?.result, null, 2) }}</pre>
  </AppDialog>
  <AppDrawer :model-value="!!selected" title="任务执行详情" size="680px" @close="close">
    <template v-if="selected">
      <dl class="detail-grid">
        <dt>任务</dt>
        <dd>{{ selected.operation }}</dd>
        <dt>ID</dt>
        <dd>{{ selected.id }}</dd>
        <dt>状态</dt>
        <dd>
          <StatusBadge
            :value="panel.tasks.find((t) => t.id === selected?.id)?.state || selected.state"
          />
        </dd>
        <dt>提交时间</dt>
        <dd>{{ date(selected.createdAt) }}</dd>
      </dl>
      <div v-if="selected.error" class="task-error">{{ selected.error }}</div>
      <el-alert v-if="error" :title="error" type="warning" :closable="false" />
      <pre
        class="log-box"
      ><template v-for="event in events" :key="event.sequence"><span class="muted">{{ new Date(event.createdAt).toLocaleTimeString('zh-CN',{hour12:false}) }}</span>  {{ event.message }}
</template><span v-if="!events.length" class="muted">等待任务输出…</span></pre>
      <div v-if="selected.state === 'interrupted'" class="notice warn">
        服务重启导致任务中断。请检查实际安装现场或资源状态，再决定重试；面板不会自动重复此操作。
      </div>
    </template>
  </AppDrawer>
</template>
