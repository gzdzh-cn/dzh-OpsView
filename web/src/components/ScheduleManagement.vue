<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { usePanel } from '../stores/panel'
import { usePageQuery } from '../composables/pageQuery'
import { usePageState } from '../composables/pageState'
import { api, post, put, action, errorText, date, type Resource, type Config } from '../lib/api'
import LoadingContent from './LoadingContent.vue'
import DataList from './DataList.vue'
import ResourceForm from './ResourceForm.vue'
import AppDialog from './AppDialog.vue'
import StatusBadge from './StatusBadge.vue'
const panel = usePanel(),
  filter = usePageState('schedule-type', 'all')
const { data, busy: reading, error, load } = usePageQuery<Config>('/schedules', { rows: [] })
const rows = computed(() =>
  data.value.rows.filter(
    (r: Config) => filter.value === 'all' || r.resource.config.operation === filter.value,
  ),
)
const editing = ref<Resource>(),
  initial = ref<Config>(),
  dialog = ref(false),
  busy = ref(false)
const logOpen = ref(false),
  logText = ref(''),
  logReading = ref(false)
const importOpen = ref(false),
  importText = ref(''),
  candidates = ref<Config[]>([]),
  importError = ref('')
function latest(row: Config) {
  return panel.tasks.find((t) => t.scheduleId === row.resource.id)
}
async function reload() {
  await Promise.all([panel.refresh(['resources', 'tasks']), load(true)])
}
function create(row?: Config, copy = false) {
  editing.value = copy ? undefined : row?.resource
  initial.value = copy ? { ...row?.resource.config, enabled: false } : undefined
  dialog.value = true
}
async function run(row: Config) {
  try {
    await action(row.resource.id, 'run')
    await reload()
    ElMessage.success('任务已提交，重复运行由资源锁阻止')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function toggle(row: Config) {
  if (busy.value) return
  busy.value = true
  try {
    const r = row.resource
    await put('/resources/' + r.id, {
      name: r.name,
      revision: r.revision,
      config: { ...r.config, enabled: !r.config.enabled },
    })
    await reload()
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    busy.value = false
  }
}
async function remove(row: Config) {
  try {
    await ElMessageBox.confirm(
      `删除计划 ${row.resource.name}？已执行的备份和运行中的任务保留。`,
      '删除计划',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' },
    )
    await api('/resources/' + row.resource.id, { method: 'DELETE' })
    await reload()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function logs(row: Config) {
  logOpen.value = true
  logText.value = ''
  logReading.value = true
  try {
    const t = latest(row)
    if (!t) {
      logText.value = '暂无执行记录（任务中心保留最近 200 条）'
      return
    }
    const events = await api<Config[]>('/tasks/' + t.id + '/events')
    logText.value =
      `${t.operation} · ${t.state}\n${t.error || ''}\n` +
      events.map((e) => `${date(e.createdAt)}  ${e.message}`).join('\n')
  } catch (e) {
    logText.value = errorText(e)
  } finally {
    logReading.value = false
  }
}
function exportSchedules() {
  const content = {
    format: 'opsview-schedules',
    version: 1,
    schedules: rows.value.map((r: Config) => ({
      name: r.resource.name,
      config: {
        targetId: r.resource.config.targetId,
        operation: r.resource.config.operation,
        cron: r.resource.config.cron,
        enabled: r.resource.config.enabled,
      },
    })),
  }
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(content, null, 2)], { type: 'application/json' }),
  )
  const a = document.createElement('a')
  a.href = url
  a.download = 'opsview-schedules.json'
  a.click()
  URL.revokeObjectURL(url)
}
function preview() {
  candidates.value = []
  importError.value = ''
  try {
    const value = JSON.parse(importText.value)
    if (
      value.format !== 'opsview-schedules' ||
      value.version !== 1 ||
      !Array.isArray(value.schedules) ||
      value.schedules.length > 100
    )
      throw new Error('仅支持 OpsView v1 计划格式，每次最多 100 条')
    candidates.value = value.schedules.map((r: Config) => {
      if (
        Object.keys(r).some((k) => !['name', 'config'].includes(k)) ||
        !r.config ||
        Object.keys(r.config).some((k) => !['targetId', 'operation', 'cron', 'enabled'].includes(k))
      )
        throw new Error('包含不支持的字段，请核对后再导入')
      if (
        !r.name ||
        !['backup', 'renew'].includes(r.config.operation) ||
        typeof r.config.cron !== 'string'
      )
        throw new Error('计划名称、类型或周期无效')
      const target = panel.resources.find((x) => x.id === r.config.targetId)
      if (!target || target.kind !== (r.config.operation === 'backup' ? 'backup' : 'site'))
        throw new Error('目标不存在或类型不匹配：' + r.name)
      if (panel.kind('schedule').some((x) => x.name === r.name))
        throw new Error('名称已存在：' + r.name)
      return { name: r.name, config: { ...r.config, enabled: false } }
    })
  } catch (e) {
    importError.value = errorText(e)
  }
}
async function applyImport() {
  busy.value = true
  try {
    for (const row of [...candidates.value]) {
      await post('/resources', { kind: 'schedule', ...row })
      candidates.value.shift()
    }
    importOpen.value = false
    ElMessage.success('计划已导入，默认暂停；请核对后启用')
  } catch (e) {
    importError.value = '部分计划未导入：' + errorText(e)
  } finally {
    busy.value = false
    await reload()
  }
}
</script>
<template>
  <section class="surface">
    <div class="page-toolbar">
      <div class="page-toolbar-left">
        <el-button type="primary" @click="create()">添加计划任务</el-button>
        <el-button
          @click="
            () => {
              importOpen = true
              importText = ''
              candidates = []
              importError = ''
            }
          "
        >
          导入
        </el-button>
        <el-button :disabled="!rows.length" @click="exportSchedules">导出</el-button>
        <el-select v-model="filter" aria-label="任务类型筛选">
          <el-option label="全部类型" value="all" />
          <el-option label="备份" value="backup" />
          <el-option label="证书续期" value="renew" />
        </el-select>
      </div>
      <span class="form-help" :title="data.serverTime">
        服务器时区：{{ data.timezone || '—' }} {{ data.zone }}
      </span>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false">
      <el-button link @click="load(true)">重试</el-button>
    </el-alert>
    <LoadingContent :busy="reading || panel.pending.resources">
      <DataList :rows="rows" state-key="schedules" searchable>
        <el-table-column label="任务名称" min-width="160">
          <template #default="{ row }">{{ row.resource.name }}</template>
        </el-table-column>
        <el-table-column label="类型 / 目标" min-width="160">
          <template #default="{ row }">
            {{ row.resource.config.operation === 'backup' ? '备份' : '证书续期' }}
            <small class="cell-subtext">{{ row.targetName || '目标已移除' }}</small>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-button
              link
              :type="row.resource.config.enabled ? 'success' : 'info'"
              :disabled="busy"
              @click="toggle(row)"
            >
              {{ row.resource.config.enabled ? '已启用' : '已暂停' }}
            </el-button>
          </template>
        </el-table-column>
        <el-table-column label="执行周期" min-width="130">
          <template #default="{ row }">
            <code>{{ row.resource.config.cron }}</code>
          </template>
        </el-table-column>
        <el-table-column label="下次执行" min-width="170">
          <template #default="{ row }">
            {{ row.reason || (row.nextRun ? date(row.nextRun) : '—') }}
          </template>
        </el-table-column>
        <el-table-column label="上次执行 / 结果" min-width="185">
          <template #default="{ row }">
            <template v-if="latest(row)">
              {{ date(latest(row)?.createdAt) }}
              <small class="cell-subtext"><StatusBadge :value="latest(row)!.state" /></small>
            </template>
            <span v-else>—</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="230" fixed="right">
          <template #default="{ row }">
            <div class="row-actions">
              <button @click="run(row)">执行</button>
              <button @click="create(row)">编辑</button>
              <button @click="logs(row)">日志</button>
              <button @click="create(row, true)">复制</button>
              <button class="danger" @click="remove(row)">删除</button>
            </div>
          </template>
        </el-table-column>
      </DataList>
    </LoadingContent>
  </section>
  <ResourceForm
    v-model="dialog"
    kind="schedule"
    :editing="editing"
    :initial="initial"
    @saved="reload"
  />
  <AppDialog v-model="logOpen" title="计划执行日志" width="850px">
    <LoadingContent :busy="logReading">
      <pre class="log-box">{{ logText }}</pre>
    </LoadingContent>
    <template #footer>
      <el-button @click="logOpen = false">关闭</el-button>
      <router-link to="/tasks"><el-button type="primary">任务中心实时日志</el-button></router-link>
    </template>
  </AppDialog>
  <AppDialog v-model="importOpen" title="导入计划任务" width="760px" :close-on-click-modal="false">
    <el-alert
      title="跨服务器导入前请核对目标 ID。导入后全部暂停，不自动执行；不导入凭据或任意 Shell。"
      type="info"
      :closable="false"
    />
    <el-input
      v-model="importText"
      type="textarea"
      :rows="8"
      placeholder="粘贴 OpsView 计划 JSON"
      @input="candidates = []"
    />
    <el-alert v-if="importError" :title="importError" type="error" :closable="false" />
    <DataList v-if="candidates.length" :rows="candidates">
      <el-table-column prop="name" label="名称" />
      <el-table-column label="Cron">
        <template #default="{ row }">{{ row.config.cron }}</template>
      </el-table-column>
      <el-table-column label="状态"><template #default>导入后暂停</template></el-table-column>
    </DataList>
    <template #footer>
      <el-button :disabled="busy" @click="importOpen = false">取消</el-button>
      <el-button @click="preview">预览</el-button>
      <el-button type="primary" :disabled="!candidates.length" :loading="busy" @click="applyImport">
        导入 {{ candidates.length }} 条
      </el-button>
    </template>
  </AppDialog>
</template>
