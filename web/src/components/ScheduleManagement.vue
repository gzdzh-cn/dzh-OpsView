<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { usePanel } from '../stores/panel'
import { usePageQuery } from '../composables/pageQuery'
import { usePageState } from '../composables/pageState'
import { api, post, put, action, errorText, date, type Resource, type Config } from '../lib/api'
import LoadingContent from './LoadingContent.vue'
import DataList from './DataList.vue'
import ScheduleEditor from './ScheduleEditor.vue'
import { identity, saveJSON } from '../lib/identity'
import AppDialog from './AppDialog.vue'
import StatusBadge from './StatusBadge.vue'
import ScheduleHistory from './ScheduleHistory.vue'
const panel = usePanel(),
  filter = usePageState('schedule-type', 'all')
const { data, busy: reading, error, load, hasData } = usePageQuery<Config>('/schedules', { rows: [] })
const category = usePageState('schedule-category', 'all')
const categoryOpen=ref(false),categoryNames=ref<string[]>([]),newCategory=ref('')
async function manageCategories(){try{categoryNames.value=await api('/schedule-categories');categoryOpen.value=true}catch(e){ElMessage.error(errorText(e))}}
async function categoryAction(old='',remove=false){try{let name=newCategory.value;if(old&&!remove){name=(await ElMessageBox.prompt('输入新的分类名称','修改分类',{inputValue:old,confirmButtonText:'保存',cancelButtonText:'取消'})).value};if(remove)await ElMessageBox.confirm('删除分类后，任务移入默认分类，任务内容保持。','删除分类',{confirmButtonText:'删除',cancelButtonText:'取消'});await post('/schedule-categories',{old,name,remove});newCategory.value='';categoryNames.value=await api('/schedule-categories');await reload()}catch(e){if(e!=='cancel'&&e!=='close')ElMessage.error(errorText(e))}}
const categories = computed(() => [
  ...new Set<string>(data.value.rows.map((r: Config) => r.resource.config.category || '默认分类')),
])
const rows = computed(() =>
  data.value.rows
    .filter(
      (r: Config) =>
        (status.value === 'all' ||
          (r.resource.config.enabled ? 'enabled' : 'paused') === status.value) &&
        (!search.value ||
          JSON.stringify([r.resource.name, r.resource.config.category, r.targetName])
            .toLowerCase()
            .includes(search.value.toLowerCase())) &&
        (filter.value === 'all' || r.resource.config.operation === filter.value) &&
        (category.value === 'all' || (r.resource.config.category || '默认分类') === category.value),
    )
    .sort(
      (a: Config, b: Config) =>
        Number(!!b.resource.config.pinned) - Number(!!a.resource.config.pinned),
    ),
)
const editing = ref<Resource>(),
  initial = ref<Config>(),
  dialog = ref(false),
  busy = ref(false)
const historyId = ref(''),
  status = usePageState('schedule-status', 'all'),
  search = usePageState('schedule-search', ''),
  selected = ref<Config[]>([])
const importOpen = ref(false),
  importText = ref(''),
  candidates = ref<Config[]>([]),
  importError = ref('')
function latest(row: Config) {
  return (
    panel.tasks.find((t) => t.scheduleId === row.resource.id) ||
    (row.lastRun?.id ? row.lastRun : undefined)
  )
}
async function reload() {
  await Promise.all([panel.refresh(['resources', 'tasks']), load(true)])
}
async function create(row?: Config, copy = false) {
  try {
    editing.value = copy ? undefined : row?.resource
    initial.value =
      copy && row
        ? { ...(await api<Resource>('/schedules/' + row.resource.id)).config, enabled: false }
        : undefined
    dialog.value = true
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function run(row: Config) {
  try {
    await action(
      row.resource.id,
      'run',
      row.resource.config.user === 'root' ? await identity() : {},
    )
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
    const auth = r.config.user === 'root' && !r.config.enabled ? await identity() : undefined
    await api('/resources/' + r.id, {
      method: 'PUT',
      headers: auth ? { 'X-OpsView-Password': auth.password, 'X-OpsView-TOTP': auth.code } : {},
      body: JSON.stringify({
        name: r.name,
        revision: r.revision,
        config: { ...r.config, enabled: !r.config.enabled },
      }),
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
      `删除计划 ${row.resource.name}？已执行的备份保留；运行中的任务请先停止。`,
      '删除计划',
      { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' },
    )
    await api('/resources/' + row.resource.id, { method: 'DELETE' })
    await reload()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
function logs(row: Config) {
  historyId.value = row.resource.id
}
async function exportSchedules() {
  try {
    const schedules = await Promise.all(
      rows.value.map((r: Config) => api('/schedules/' + r.resource.id + '/export')),
    )
    saveJSON({ format: 'opsview-schedules', version: 2, schedules }, 'opsview-schedules.json')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function stop(row: Config) {
  try {
    await post('/schedules/' + row.resource.id + '/stop')
    await reload()
    ElMessage.success('已提交停止请求')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function pin(row: Config) {
  try {
    const r = await api<Resource>('/schedules/' + row.resource.id)
    await put('/resources/' + r.id, {
      name: r.name,
      revision: r.revision,
      config: { ...r.config, pinned: !r.config.pinned },
    })
    await reload()
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
function preview() {
  candidates.value = []
  importError.value = ''
  try {
    const value = JSON.parse(importText.value)
    if (
      value.format !== 'opsview-schedules' ||
      ![1, 2].includes(value.version) ||
      !Array.isArray(value.schedules) ||
      value.schedules.length > 100
    )
      throw new Error('仅支持 OpsView 计划格式，每次最多 100 条')
    candidates.value = value.schedules.map((r: Config) => {
      if (
        !r.name ||
        !r.config ||
        typeof r.config.cron !== 'string' ||
        ![
          'backup',
          'renew',
          'shell',
          'python',
          'http',
          'directory',
          'panel',
          'rotate',
          'cleanup',
          'site-state',
          'time-sync',
        ].includes(r.config.operation)
      )
        throw new Error('计划名称、类型或周期无效')
      delete r.config.rootApproval
      delete r.config.environment
      delete r.config.headers
      delete r.config.body
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
    await post('/schedules/import', {
      schedules: candidates.value.map((r) => ({ name: r.name, config: r.config })),
      preview: false,
    })
    importOpen.value = false
    ElMessage.success('计划已导入，全部暂停；root 任务启用时需验证')
  } catch (e) {
    importError.value = errorText(e)
  } finally {
    busy.value = false
    await reload()
  }
}
async function batch(command: string) {
  if (!selected.value.length) {
    ElMessage.warning('请先勾选任务')
    return
  }
  try {
    await ElMessageBox.confirm(
      '对明确勾选的 ' +
        selected.value.length +
        ' 条计划执行' +
        (command === 'delete' ? '删除' : '暂停') +
        '？',
      '批量操作',
      { confirmButtonText: '确认', cancelButtonText: '取消', type: 'warning' },
    )
    for (const row of selected.value) {
      if (command === 'delete') await api('/resources/' + row.resource.id, { method: 'DELETE' })
      else {
        const r = await api<Resource>('/schedules/' + row.resource.id)
        await put('/resources/' + r.id, {
          name: r.name,
          revision: r.revision,
          config: { ...r.config, enabled: false },
        })
      }
    }
    selected.value = []
    await reload()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
    await reload()
  }
}
function more(command: string) {
 if(command==='categories'){void manageCategories();return}
  if (command === 'import') {
    importOpen.value = true
    importText.value = ''
    candidates.value = []
    importError.value = ''
  } else if (command === 'export') void exportSchedules()
  else void batch(command)
}
</script>
<template>
  <section class="surface">
    <div class="page-toolbar">
      <div class="page-toolbar-left">
        <el-button type="primary" @click="create()">添加计划任务</el-button>
        <el-dropdown @command="more">
          <el-button>更多 ▾</el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="categories">分类管理</el-dropdown-item><el-dropdown-item command="import">导入</el-dropdown-item>
              <el-dropdown-item command="export">导出当前筛选</el-dropdown-item>
              <el-dropdown-item command="pause">暂停勾选任务</el-dropdown-item>
              <el-dropdown-item command="delete">删除勾选任务</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-select v-model="filter" aria-label="任务类型筛选">
          <el-option label="全部类型" value="all" />
          <el-option label="备份" value="backup" />
          <el-option label="证书续期" value="renew" />
          <el-option
            v-for="(label, key) in {
              shell: 'Shell 脚本',
              python: 'Python 脚本',
              http: 'HTTP 请求',
              directory: '目录备份',
              panel: '面板备份',
              rotate: '日志切割',
              cleanup: '日志清理',
              'site-state': '网站启停',
              'time-sync': '时间同步',
            }"
            :key="key"
            :label="label"
            :value="key"
          />
        </el-select>
        <el-select v-model="category" aria-label="任务分类">
          <el-option label="全部分类" value="all" />
          <el-option v-for="c in categories" :key="c" :label="c" :value="c" />
        </el-select>
        <el-select v-model="status" aria-label="任务状态筛选">
          <el-option value="all" label="全部状态" />
          <el-option value="enabled" label="已启用" />
          <el-option value="paused" label="已暂停" />
        </el-select>
        <el-input
          v-model="search"
          clearable
          placeholder="搜索任务、分类或目标"
          aria-label="搜索任务"
        />
      </div>
      <span class="form-help" :title="data.serverTime">
        服务器时区：{{ data.timezone || '—' }} {{ data.zone }}
      </span>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false">
      <el-button link @click="load(true)">重试</el-button>
    </el-alert>
    <LoadingContent :busy="reading || panel.pending.resources" :ready="hasData && panel.hasData.resources">
      <DataList :rows="rows" state-key="schedules" @selection-change="selected = $event">
        <el-table-column type="selection" width="40" />
        <el-table-column label="任务名称" min-width="160">
          <template #default="{ row }">
            <button
              class="text-action"
              :title="row.resource.config.pinned ? '取消置顶' : '置顶'"
              @click="pin(row)"
            >
              {{ row.resource.config.pinned ? '★' : '☆' }}
            </button>
            <span>{{ row.resource.name }}</span>
            <small class="cell-subtext">
              {{ row.resource.config.operation }} {{ row.targetName }}
            </small>
          </template>
        </el-table-column>
        <el-table-column label="分类" min-width="100">
          <template #default="{ row }">{{ row.resource.config.category || '默认分类' }}</template>
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
        <el-table-column label="执行用户" width="120">
          <template #default="{ row }">{{ row.resource.config.user || '系统任务' }}</template>
        </el-table-column>
        <el-table-column label="保留策略" width="100">
          <template #default="{ row }">
            {{ row.resource.config.retentionDays || 30 }} 天日志
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
              <button
                v-if="!latest(row) || !['running', 'queued'].includes(latest(row)!.state)"
                @click="run(row)"
              >
                执行
              </button>
              <button v-else @click="stop(row)">停止</button>
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
  <AppDialog v-model="categoryOpen" title="计划分类管理" width="650px" size="small"><div class="page-toolbar"><el-input v-model="newCategory" placeholder="新分类名称" maxlength="40"/><el-button type="primary" @click="categoryAction()">添加分类</el-button></div><DataList :rows="categoryNames.map(name=>({name}))"><el-table-column prop="name" label="分类"/><el-table-column label="操作"><template #default="{row}"><div v-if="row.name!=='默认分类'" class="row-actions"><button @click="categoryAction(row.name)">修改</button><button class="danger" @click="categoryAction(row.name,true)">删除</button></div></template></el-table-column></DataList><template #footer><el-button @click="categoryOpen=false">关闭</el-button></template></AppDialog>
 <ScheduleEditor v-model="dialog" :editing="editing" :initial="initial" @saved="reload" />
  <ScheduleHistory v-model="historyId" />
  <AppDialog v-model="importOpen" title="导入计划任务" width="760px" :close-on-click-modal="false">
    <el-alert
      title="跨服务器导入前请核对目标 ID。导入后全部暂停，私密变量及凭据不导入；root 脚本需重新确认。"
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
