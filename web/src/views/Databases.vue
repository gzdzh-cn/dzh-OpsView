<script setup lang="ts">
import LoadingContent from '../components/LoadingContent.vue'
import DialogActions from '../components/DialogActions.vue'
import AppDialog from '../components/AppDialog.vue'
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { usePanel } from '../stores/panel'
import { usePageState } from '../composables/pageState'
import { usePageQuery } from '../composables/pageQuery'
import { api, post, action, errorText, date, size, type Resource, type Config } from '../lib/api'
import DataList from '../components/DataList.vue'
import ResourceForm from '../components/ResourceForm.vue'
import SoftwareSettings from '../components/SoftwareSettings.vue'
import RedisData from '../components/RedisData.vue'
const panel = usePanel(),
  route = useRoute()
const tab = usePageState('engine', String(route.query.tab || 'mysql'))
if (!['mysql', 'postgresql', 'redis'].includes(tab.value)) tab.value = 'mysql'
const instanceId = usePageState(() => 'instance:' + tab.value, ''),
  search = usePageState(() => 'search:' + tab.value, '')
const instances = computed(() =>
  panel
    .kind('instance')
    .filter((i) => i.config.software === tab.value && i.status !== 'uninstalled'),
)
watch(
  instances,
  () => {
    if (!instances.value.some((i) => i.id === instanceId.value))
      instanceId.value = instances.value[0]?.id || ''
  },
  { immediate: true },
)
const instance = computed(() => instances.value.find((i) => i.id === instanceId.value))
const list = usePageQuery<Config>(
  () =>
    tab.value !== 'redis' ? `/databases?engine=${tab.value}&instanceId=${instanceId.value}` : '',
  { rows: [] },
)
const capability = usePageQuery<Config>(
  () =>
    instanceId.value && tab.value !== 'redis' ? `/databases/${instanceId.value}/capabilities` : '',
  {},
)
const rows = computed<Resource[]>(() =>
  list.data.value.rows.filter((r: Resource) =>
    (r.name + ' ' + r.config.username + ' ' + (r.config.note || ''))
      .toLowerCase()
      .includes(search.value.toLowerCase()),
  ),
)
const selected = ref<Resource[]>([]),
  current = ref<Resource>(),
  settings = ref(false),
  create = ref(false),
  busy = ref(false),
  error = ref(''),
  mode = ref(''),
  payload = ref<Config>({}),
  read = ref<Config>({}),
  inventory = ref<Config[]>([]),
  tables = ref<Config[]>([]),
  recycled = ref<Resource[]>([])
const backupForm = ref(false),
  formKind = ref('backup'),
  formInitial = ref<Config>({})
const secret = ref(''),
  identity = ref({ password: '', code: '' })
let secretTimer: ReturnType<typeof setTimeout> | undefined
const activeBackups = computed(() =>
  panel
    .kind('backup')
    .filter((b) => b.config.targetId === current.value?.id && !b.config.recycleOnly),
)
const titles: Record<string, string> = {
  sync: '同步数据库',
  password: '修改业务账号密码',
  credential: '查看业务账号密码',
  grant: '账号权限',
  access: '连接来源',
  tables: '数据库工具',
  backup: '备份与恢复',
  recycle: '删除并进入回收站',
  trash: '数据库回收站',
  restore: '恢复数据库',
  connect: '连接信息',
  import: '导入 SQL',
  migrate: '迁移数据库',
  structure: '表结构',
}
function clearSecret() {
  secret.value = ''
  identity.value = { password: '', code: '' }
  clearTimeout(secretTimer)
}
watch([tab, instanceId], () => {
  selected.value = []
  mode.value = ''
  clearSecret()
})
let modalSequence = 0
watch(
  mode,
  () => {
    modalSequence++
    clearSecret()
    if (!mode.value) {
      payload.value = {}
      read.value = {}
      structureOpen.value = false
    }
  },
  { flush: 'sync' },
)
onBeforeUnmount(() => {
  modalSequence++
  clearSecret()
  payload.value = {}
  read.value = {}
})
watch(
  () => panel.tasks.map((t) => t.id + t.state).join(','),
  () => {
    void list.load(true)
  },
)
async function refresh() {
  await Promise.all([panel.refresh(['resources', 'tasks']), list.load(true)])
}
async function perform(fn: () => Promise<void>) {
  busy.value = true
  error.value = ''
  try {
    await fn()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function task(r: Resource, op: string, p: Config = {}) {
  await post(`/databases/${r.id}/${op}`, p)
  ElMessage.success('任务已提交，可在任务中心查看')
  await refresh()
}
async function open(name: string, r?: Resource) {
  current.value = r || instance.value
  mode.value = name
  const seq = ++modalSequence
  payload.value = {}
  read.value = {}
  error.value = ''
  tables.value = []
  inventory.value = []
  if (name === 'grant')
    payload.value = {
      level: r?.config.privilege || 'admin',
      source: r?.config.source,
      username: r?.config.username,
    }
  if (name === 'access')
    payload.value = {
      source: r?.config.source || '127.0.0.1',
      allowRemote: !!r?.config.allowRemote,
    }
  if (name === 'sync')
    await perform(async () => {
      const result = await api(`/databases/${instanceId.value}/inventory`)
      if (seq === modalSequence)
        inventory.value = result.rows.filter(
          (row: Config) => !rows.value.some((db) => db.name === row.name),
        )
    })
  if (name === 'tables')
    await perform(async () => {
      const result = await api(`/databases/${r?.id}/tables`)
      if (seq === modalSequence) tables.value = result.rows
    })
  if (name === 'grant')
    await perform(async () => {
      const result = await api(`/databases/${r?.id}/permissions`)
      if (seq === modalSequence) read.value = result
    })
  if (name === 'trash') await loadTrash()
}
async function loadTrash() {
  await perform(async () => {
    recycled.value = (
      await api(`/databases?engine=${tab.value}&instanceId=${instanceId.value}&recycled=true`)
    ).rows
  })
}
async function reveal() {
  const seq = modalSequence
  await perform(async () => {
    const result = await post(`/databases/${current.value?.id}/credential`, identity.value)
    if (seq !== modalSequence || mode.value !== 'credential') return
    clearTimeout(secretTimer)
    secret.value = result.password
    identity.value = { password: '', code: '' }
    secretTimer = setTimeout(clearSecret, 30000)
  })
}
async function copy(value: string) {
  try {
    await navigator.clipboard.writeText(value)
    ElMessage.success('已复制')
  } catch {
    ElMessage.warning('当前浏览器无法访问剪贴板，请手动复制')
  }
}
async function saveNote(r: Resource) {
  await perform(async () => {
    const v = await ElMessageBox.prompt('修改备注', '备注', { inputValue: r.config.note || '' })
    await post(`/databases/${r.id}/note`, { note: v.value, revision: r.revision })
    await refresh()
  })
}
async function submit() {
  if (!current.value) return
  await perform(async () => {
    const r = current.value!
    if (mode.value === 'credential') {
      await reveal()
      return
    }
    if (mode.value === 'import') {
      await action(r.id, 'import', { filename: payload.value.filename })
    } else if (mode.value === 'migrate') {
      await action(r.id, 'migrate', {
        targetInstanceId: payload.value.targetInstanceId,
        maintenanceConfirmed: payload.value.maintenanceConfirmed,
      })
    } else
      await task(r, mode.value, { ...payload.value, confirmDataDeletion: mode.value === 'recycle' })
    mode.value = ''
    await refresh()
  })
}
function newBackup(target?: Resource) {
  formKind.value = 'backup'
  formInitial.value = { targetId: (target || current.value || instance.value)?.id }
  backupForm.value = true
}
function schedule(b: Resource) {
  formKind.value = 'schedule'
  formInitial.value = { targetId: b.id, operation: 'backup' }
  backupForm.value = true
}
async function backupAction(b: Resource, op: string, artifact?: string) {
  await perform(async () => {
    const p: Config = { artifact }
    if (op === 'restore') {
      const target = panel.resources.find((r) => r.id === b.config.targetId)
      if (target?.kind === 'instance') {
        const answer = await ElMessageBox.prompt(
          '输入已启动、为空且无业务写入的原生 Redis 目标实例 ID',
          '恢复整个实例',
        )
        p.targetInstanceId = answer.value
        await ElMessageBox.confirm('确认目标为空且处于维护窗口？', '恢复确认')
        p.maintenanceConfirmed = true
      } else {
        const answer = await ElMessageBox.prompt('恢复为新数据库名，不覆盖原库', '恢复')
        p.databaseName = answer.value
      }
    }
    await action(b.id, op, p)
    ElMessage.success('任务已提交')
    await refresh()
  })
}
async function recycleAction(r: Resource, op: string) {
  await perform(async () => {
    const p: Config = { confirmed: true }
    if (op === 'restore') {
      current.value = r
      mode.value = 'restore'
      payload.value = { targetInstanceId: instanceId.value, databaseName: '' }
      return
    }
    await ElMessageBox.confirm(`永久删除 ${r.name} 的回收记录和专属备份？`, '永久删除', {
      type: 'warning',
    })
    await task(r, op, p)
    await loadTrash()
  })
}
async function clearTrash() {
  const scope = [...recycled.value]
  if (!scope.length) return
  await perform(async () => {
    await ElMessageBox.confirm(
      `永久删除当前实例的 ${scope.length} 条回收记录及其专属备份？业务数据库不会删除。`,
      '清空回收站',
      { type: 'warning' },
    )
    for (const r of scope) await post(`/databases/${r.id}/purge`, { confirmed: true })
    ElMessage.success('清理任务已提交')
    await refresh()
  })
}
async function maintain(t: Config, operation: string) {
  if (!current.value) return
  await perform(async () => {
    await ElMessageBox.confirm(
      `对 ${t.schema}.${t.name} 执行 ${operation}，可能增加负载或等待锁。`,
      '确认表维护',
    )
    await task(current.value!, 'maintain', {
      schema: t.schema,
      table: t.name,
      operation,
      confirmed: true,
    })
  })
}
async function structure(t: Config) {
  await perform(async () => {
    read.value = await api(
      `/databases/${current.value?.id}/structure?` +
        new URLSearchParams({ table: t.name, schema: t.schema }),
    )
    structureOpen.value = true
  })
}
const structureOpen = ref(false)
function downloadText() {
  const url = URL.createObjectURL(new Blob([read.value.content || ''], { type: 'text/plain' }))
  const a = document.createElement('a')
  a.href = url
  a.download = 'table-structure.sql'
  a.click()
  URL.revokeObjectURL(url)
}
function backupSummary(r: Resource) {
  const bs = panel
    .kind('backup')
    .filter((b) => b.config.targetId === r.id && !b.config.recycleOnly && b.config.lastBackupAt)
    .sort((a, b) => String(b.config.lastBackupAt).localeCompare(a.config.lastBackupAt))
  return bs.length ? date(bs[0].config.lastBackupAt) : '未备份'
}
async function batchBackup() {
  await perform(async () => {
    for (const r of selected.value) {
      let b = panel.kind('backup').find((b) => b.config.targetId === r.id && !b.config.recycleOnly)
      if (!b)
        b = await post<Resource>('/resources', {
          kind: 'backup',
          name: 'db-backup-' + r.id,
          config: { targetId: r.id, destination: 'local', retention: 7 },
        })
      await action(b!.id, 'backup')
    }
    ElMessage.success('已提交所选数据库备份')
    await refresh()
  })
}
</script>
<template>
  <section class="surface databases-page">
    <div class="page-toolbar">
      <div class="page-toolbar-left">
        <div class="tab-bar" role="tablist" aria-label="数据库类型">
          <button
            v-for="[key, label] in [
              ['mysql', 'MySQL'],
              ['postgresql', 'PostgreSQL'],
              ['redis', 'Redis'],
            ]"
            :key="key"
            role="tab"
            :aria-selected="tab === key"
            :class="{ active: tab === key }"
            @click="tab = key"
          >
            {{ label }}
          </button>
        </div>
        <el-select
          v-if="instances.length"
          v-model="instanceId"
          aria-label="数据库实例"
          style="width: 270px"
        >
          <el-option
            v-for="i in instances"
            :key="i.id"
            :value="i.id"
            :label="`${i.name} · ${i.config.version} · :${i.config.port} · ${i.status}`"
          />
        </el-select>
      </div>
      <div v-if="instance" class="page-toolbar-right">
        <template v-if="tab !== 'redis'">
          <el-button type="primary" @click="create = true">添加数据库</el-button>
          <el-button @click="open('sync')">同步数据库</el-button>
          <el-button :disabled="!rows.length" @click="newBackup(rows[0])">备份计划</el-button>
        </template>
        <el-dropdown
          @command="(cmd: string) => (cmd === 'settings' ? (settings = true) : open(cmd))"
        >
          <el-button>更多</el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="settings">实例设置</el-dropdown-item>
              <el-dropdown-item v-if="tab !== 'redis'" command="password">
                管理员密码
              </el-dropdown-item>
              <el-dropdown-item command="connect">连接信息</el-dropdown-item>
              <el-dropdown-item v-if="tab !== 'redis'" command="trash">回收站</el-dropdown-item>
              <el-dropdown-item v-else command="backup">实例备份</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <LoadingContent :busy="panel.pending.resources">
      <el-empty v-if="!instance" description="尚未安装对应数据库实例">
        <router-link :to="`/software?software=${tab}`">
          <el-button type="primary">前往软件市场安装</el-button>
        </router-link>
      </el-empty>
      <template v-else>
        <el-alert
          v-if="!['ready', 'running'].includes(instance.status)"
          title="实例未就绪，可在实例设置中检查服务"
          type="warning"
          :closable="false"
        />
        <el-alert
          v-if="capability.error.value && tab !== 'redis'"
          :title="capability.error.value"
          type="warning"
          :closable="false"
        >
          <el-button link @click="capability.load(true)">重新连接</el-button>
        </el-alert>
        <RedisData v-if="tab === 'redis'" :key="instanceId" :instance="instance" />
        <template v-else>
          <div class="page-toolbar">
            <el-input
              v-model="search"
              aria-label="搜索数据库"
              placeholder="数据库名 / 用户名 / 备注"
              clearable
              style="max-width: 300px"
            />
            <el-button v-if="selected.length" :loading="busy" @click="batchBackup">
              备份所选 {{ selected.length }} 项
            </el-button>
          </div>
          <el-alert
            v-if="list.error.value"
            :title="list.error.value"
            type="error"
            :closable="false"
          >
            <el-button link @click="list.load(true)">重试</el-button>
          </el-alert>
          <DataList
            :rows="rows"
            :state-key="`databases:${tab}:${instanceId}`"
            v-loading="list.busy.value"
            @selection-change="(v: Resource[]) => (selected = v)"
          >
            <el-table-column type="selection" width="42" />
            <el-table-column prop="name" label="数据库名" min-width="160">
              <template #default="{ row }">
                {{ row.name }}
                <small v-if="row.status !== 'ready'" class="cell-subtext">{{ row.status }}</small>
              </template>
            </el-table-column>
            <el-table-column :label="tab === 'postgresql' ? '所有者' : '用户名'" min-width="120">
              <template #default="{ row }">
                {{
                  tab === 'postgresql'
                    ? row.config.owner || row.config.username
                    : row.config.username
                }}
                <small
                  v-if="tab === 'postgresql' && row.config.owner !== row.config.username"
                  class="cell-subtext"
                >
                  连接账号：{{ row.config.username }}
                </small>
              </template>
            </el-table-column>
            <el-table-column label="密码" width="110">
              <template #default="{ row }">
                <button
                  :disabled="!row.config.hasPassword"
                  class="text-link"
                  @click="open('credential', row)"
                >
                  {{ row.config.hasPassword ? '•••••• 查看' : '未保存' }}
                </button>
              </template>
            </el-table-column>
            <el-table-column label="备份" min-width="165">
              <template #default="{ row }">
                <button class="text-link" @click="open('backup', row)">
                  {{ backupSummary(row) }}
                </button>
              </template>
            </el-table-column>
            <el-table-column label="所属实例" min-width="145">
              <template #default="{ row }">{{ row.config.instanceName }}</template>
            </el-table-column>
            <el-table-column :label="tab === 'mysql' ? '字符集/排序规则' : '编码'" min-width="150">
              <template #default="{ row }">
                {{ row.config.encoding || '待同步' }}
                <small v-if="tab === 'mysql'" class="cell-subtext">
                  {{ row.config.collation || '实例默认' }}
                </small>
              </template>
            </el-table-column>
            <el-table-column label="备注" min-width="120">
              <template #default="{ row }">
                <button class="text-link" @click="saveNote(row)">
                  {{ row.config.note || '添加备注' }}
                </button>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="220" fixed="right">
              <template #default="{ row }">
                <div class="row-actions">
                  <button @click="open('tables', row)">管理</button>
                  <button @click="open('grant', row)">权限</button>
                  <button @click="open('password', row)">改密</button>
                  <el-dropdown @command="(cmd: string) => open(cmd, row)">
                    <button aria-label="更多数据库操作">更多</button>
                    <template #dropdown>
                      <el-dropdown-menu>
                        <el-dropdown-item command="connect">连接信息</el-dropdown-item>
                        <el-dropdown-item command="access">连接来源</el-dropdown-item>
                        <el-dropdown-item command="import">导入 SQL</el-dropdown-item>
                        <el-dropdown-item command="migrate">迁移</el-dropdown-item>
                        <el-dropdown-item command="recycle">删除并回收</el-dropdown-item>
                      </el-dropdown-menu>
                    </template>
                  </el-dropdown>
                </div>
              </template>
            </el-table-column>
          </DataList>
        </template>
      </template>
    </LoadingContent>
  </section>
  <ResourceForm
    v-model="create"
    kind="database"
    :initial="{ instanceId, softwareFilter: tab }"
    @saved="refresh"
  />
  <ResourceForm v-model="backupForm" :kind="formKind" :initial="formInitial" @saved="refresh" />
  <SoftwareSettings v-model="settings" :resource="instance" />
  <AppDialog
    :model-value="!!mode"
    :title="`${titles[mode] || mode}${current ? ' · ' + current.name : ''}`"
    width="min(1040px,94vw)"
    @close="mode = ''"
  >
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <div v-loading="busy">
      <template v-if="mode === 'credential'">
        <p>验证当前管理员身份后可查看业务密码，30 秒后自动隐藏。</p>
        <el-input
          v-model="identity.password"
          type="password"
          autocomplete="current-password"
          placeholder="当前管理员密码"
        />
        <el-input
          v-if="panel.auth.twoFactorEnabled"
          v-model="identity.code"
          placeholder="双因素验证码"
        />
        <DialogActions><el-button @click="reveal">验证并查看</el-button></DialogActions>
        <div v-if="secret" class="page-toolbar">
          <code>{{ secret }}</code>
          <el-button @click="copy(secret)">复制密码</el-button>
        </div>
      </template>
      <template v-else-if="mode === 'password'">
        <el-alert
          title="共享此账号的所有数据库都会受到影响，应用连接密码需同步修改。"
          type="warning"
          :closable="false"
        />
        <el-input
          v-model="payload.password"
          type="password"
          show-password
          placeholder="新密码，12–128 字节"
        />
        <el-input v-model="payload.confirmPassword" type="password" placeholder="重复新密码" />
      </template>
      <template v-else-if="mode === 'connect'">
        <pre>{{
          JSON.stringify(
            {
              host: '127.0.0.1',
              port: instance?.config.port,
              database: current?.kind === 'database' ? current.name : undefined,
              username: current?.config.username,
            },
            null,
            2,
          )
        }}</pre>
        <el-button
          @click="
            copy(
              JSON.stringify({
                host: '127.0.0.1',
                port: instance?.config.port,
                database: current?.name,
                username: current?.config.username,
              }),
            )
          "
        >
          复制连接信息（不含密码）
        </el-button>
      </template>
      <template v-else-if="mode === 'grant'">
        <pre class="db-readout">{{ read.content || JSON.stringify(read, null, 2) }}</pre>
        <el-form label-width="100px">
          <el-form-item v-if="tab === 'postgresql'" label="授权账号">
            <el-input v-model="payload.username" placeholder="普通账号名称" />
          </el-form-item>
          <el-form-item
            v-if="tab === 'postgresql' && payload.username !== current?.config.username"
            label="新账号密码"
          >
            <el-input
              v-model="payload.password"
              type="password"
              placeholder="账号不存在时需要填写"
            />
          </el-form-item>
          <el-form-item label="权限档位">
            <el-select v-model="payload.level">
              <el-option label="只读" value="readonly" />
              <el-option label="读写" value="readwrite" />
              <el-option label="库内管理" value="admin" />
            </el-select>
          </el-form-item>
        </el-form>
        <p v-if="tab === 'postgresql'">所有者保留固有权限；只读/读写请使用普通授权账号。</p>
      </template>
      <template v-else-if="mode === 'access'">
        <el-input v-model="payload.source" placeholder="来源 IP/CIDR" />
        <el-checkbox v-model="payload.allowRemote">
          确认开启远程访问，并创建限源防火墙规则
        </el-checkbox>
        <el-checkbox v-model="payload.sharedConfirmed">
          确认共享此账号的数据库一起修改来源
        </el-checkbox>
      </template>
      <template v-else-if="mode === 'sync'">
        <p>仅登记选中的现有数据库，不修改密码和权限。没有可识别账号关系的条目不能直接纳管。</p>
        <DataList :rows="inventory" state-key="db-inventory">
          <el-table-column prop="name" label="数据库名" />
          <el-table-column prop="username" label="账号" />
          <el-table-column prop="source" label="来源" />
          <el-table-column prop="encoding" label="编码" />
          <el-table-column label="操作">
            <template #default="{ row }">
              <el-button
                link
                :disabled="!row.username"
                @click="perform(() => task(instance!, 'adopt', row))"
              >
                纳管
              </el-button>
            </template>
          </el-table-column>
        </DataList>
      </template>
      <template v-else-if="mode === 'tables'">
        <DataList :rows="tables" state-key="db-tables" searchable>
          <el-table-column prop="schema" label="Schema" />
          <el-table-column prop="name" label="表名" />
          <el-table-column v-if="tab === 'mysql'" prop="engine" label="引擎" />
          <el-table-column v-if="tab === 'mysql'" prop="encoding" label="字符集/排序规则" />
          <el-table-column v-else label="索引">
            <template #default="{ row }">{{ row.indexes?.length || 0 }}</template>
          </el-table-column>
          <el-table-column prop="rows" label="估算行数" />
          <el-table-column label="大小">
            <template #default="{ row }">{{ size(row.bytes) }}</template>
          </el-table-column>
          <el-table-column prop="note" label="备注" />
          <el-table-column label="操作" min-width="210">
            <template #default="{ row }">
              <div class="row-actions">
                <button @click="structure(row)">结构</button>
                <template v-if="tab === 'mysql'">
                  <button @click="maintain(row, 'check')">检查</button>
                  <button @click="maintain(row, 'optimize')">优化</button>
                  <button
                    v-if="['MyISAM', 'ARCHIVE', 'CSV'].includes(row.engine)"
                    @click="maintain(row, 'repair')"
                  >
                    修复
                  </button>
                </template>
                <template v-else>
                  <button @click="maintain(row, 'analyze')">分析</button>
                  <button @click="maintain(row, 'vacuum')">VACUUM</button>
                </template>
              </div>
            </template>
          </el-table-column>
        </DataList>
      </template>
      <template v-else-if="mode === 'backup'">
        <el-alert
          v-if="tab === 'redis'"
          title="快照覆盖整个 Redis 实例；恢复到新的原生空实例。历史容器实例暂不支持快照备份。"
          type="info"
          :closable="false"
        />
        <div class="page-toolbar">
          <el-button
            :disabled="tab === 'redis' && instance?.config.mode !== 'native'"
            @click="newBackup()"
          >
            添加备份计划
          </el-button>
          <a v-if="current?.kind === 'database'" :href="`/api/v1/resources/${current.id}/download`">
            导出数据库
          </a>
          <el-button v-if="current?.kind === 'database'" @click="mode = 'import'">
            导入 SQL
          </el-button>
        </div>
        <DataList :rows="activeBackups" state-key="db-backups">
          <el-table-column type="expand">
            <template #default="{ row }">
              <el-table :data="row.config.history || []">
                <el-table-column prop="file" label="备份文件" min-width="240" />
                <el-table-column label="时间">
                  <template #default="{ row: artifact }">{{ date(artifact.createdAt) }}</template>
                </el-table-column>
                <el-table-column label="大小">
                  <template #default="{ row: artifact }">{{ size(artifact.size) }}</template>
                </el-table-column>
                <el-table-column label="操作">
                  <template #default="{ row: artifact }">
                    <el-button link @click="backupAction(row, 'restore', artifact.file)">
                      恢复
                    </el-button>
                    <a
                      :href="`/api/v1/resources/${row.id}/download?artifact=${encodeURIComponent(artifact.file)}`"
                    >
                      下载
                    </a>
                  </template>
                </el-table-column>
              </el-table>
            </template>
          </el-table-column>
          <el-table-column prop="name" label="备份任务" />
          <el-table-column label="最近成功">
            <template #default="{ row }">{{ date(row.config.lastBackupAt) }}</template>
          </el-table-column>
          <el-table-column label="操作" min-width="270">
            <template #default="{ row }">
              <div class="row-actions">
                <button @click="backupAction(row, 'backup')">立即备份</button>
                <button @click="schedule(row)">定时</button>
                <button v-if="row.config.artifact" @click="backupAction(row, 'restore')">
                  恢复
                </button>
                <a v-if="row.config.artifact" :href="`/api/v1/resources/${row.id}/download`">
                  下载
                </a>
              </div>
              <small
                v-for="t in panel.tasks
                  .filter((t) => t.resourceId === row.id && t.state === 'failed')
                  .slice(0, 1)"
                :key="t.id"
              >
                {{ t.error }}
              </small>
            </template>
          </el-table-column>
        </DataList>
      </template>
      <template v-else-if="mode === 'recycle'">
        <el-alert
          title="备份成功后删除数据库，回收站保留 30 天。在线备份不能保证继续写入的数据被保留。"
          type="warning"
          :closable="false"
        />
        <el-checkbox v-model="payload.maintenanceConfirmed">
          已停止业务写入，确认删除此数据库
        </el-checkbox>
      </template>
      <template v-else-if="mode === 'restore'">
        <el-form label-width="100px">
          <el-form-item label="目标实例">
            <el-select v-model="payload.targetInstanceId">
              <el-option
                v-for="i in instances"
                :key="i.id"
                :value="i.id"
                :label="i.name + ' · ' + i.config.version"
              />
            </el-select>
          </el-form-item>
          <el-form-item label="新库名称">
            <el-input
              v-model="payload.databaseName"
              placeholder="同时创建同名专用账号，拒绝覆盖已有库"
            />
          </el-form-item>
        </el-form>
        <p>仅允许同引擎、同兼容分支恢复；源回收备份保留。</p>
      </template>
      <template v-else-if="mode === 'trash'">
        <div class="page-toolbar">
          <el-button :disabled="!recycled.length" type="danger" plain @click="clearTrash">
            清空当前实例回收站
          </el-button>
          <el-button @click="loadTrash">刷新</el-button>
        </div>
        <DataList :rows="recycled" state-key="db-trash">
          <el-table-column prop="name" label="数据库" />
          <el-table-column label="保留至">
            <template #default="{ row }">{{ date(row.config.expiresAt) }}</template>
          </el-table-column>
          <el-table-column label="操作">
            <template #default="{ row }">
              <div class="row-actions">
                <button @click="recycleAction(row, 'restore')">恢复为新库</button>
                <button class="danger" @click="recycleAction(row, 'purge')">永久删除</button>
              </div>
            </template>
          </el-table-column>
        </DataList>
      </template>
      <template v-else-if="mode === 'import'">
        <p>
          先在文件管理中上传到对应实例 imports 目录。SQL 将在当前库中执行，请先备份并确认来源可信。
        </p>
        <el-input v-model="payload.filename" placeholder="backup.sql" />
      </template>
      <template v-else-if="mode === 'migrate'">
        <el-select v-model="payload.targetInstanceId" placeholder="选择另一个同引擎实例">
          <el-option
            v-for="i in instances.filter((i) => i.id !== instanceId)"
            :key="i.id"
            :value="i.id"
            :label="i.name + ' · ' + i.config.version"
            :disabled="
              tab === 'mysql'
                ? !String(i.config.version).startsWith('8.4.') ||
                  !String(instance?.config.version).startsWith('8.4.')
                : String(i.config.version).split('.')[0] !==
                  String(instance?.config.version).split('.')[0]
            "
          />
        </el-select>
        <p>MySQL 仅验证 8.4 分支内迁移；PostgreSQL 仅同大版本且只含 public schema 的表。</p>
        <el-checkbox v-model="payload.maintenanceConfirmed">
          已停止业务写入并确认维护窗口；源库保留
        </el-checkbox>
      </template>
    </div>
    <template #footer>
      <el-button @click="mode = ''">关闭</el-button>
      <el-button
        v-if="
          ['password', 'grant', 'access', 'recycle', 'restore', 'import', 'migrate'].includes(mode)
        "
        type="primary"
        :loading="busy"
        :disabled="
          (mode === 'password' &&
            (!payload.password || payload.password !== payload.confirmPassword)) ||
          (mode === 'recycle' && !payload.maintenanceConfirmed)
        "
        @click="submit"
      >
        确认提交
      </el-button>
    </template>
  </AppDialog>
  <AppDialog v-model="structureOpen" title="表结构" width="min(900px,94vw)">
    <pre class="db-readout">{{ read.content }}</pre>
    <pre v-if="read.indexes">{{ read.indexes }}</pre>
    <DialogActions><el-button @click="downloadText">导出结构</el-button></DialogActions>
  </AppDialog>
</template>
<style scoped>
.text-link {
  border: 0;
  background: transparent;
  padding: 0;
  color: var(--el-color-primary);
  cursor: pointer;
  font: inherit;
}
.text-link:disabled {
  color: var(--el-text-color-secondary);
  cursor: default;
}
.text-link:focus-visible {
  outline: 2px solid var(--el-color-primary);
  outline-offset: 3px;
}

.db-readout {
  max-height: 360px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: var(--el-fill-color-light);
  padding: 12px;
}
.tab-bar {
  display: flex;
  gap: 4px;
}
.tab-bar button {
  padding: 8px 14px;
  border: 0;
  background: none;
  color: var(--el-text-color-regular);
  cursor: pointer;
  border-bottom: 2px solid transparent;
}
.tab-bar button.active {
  color: var(--el-color-primary);
  border-bottom-color: var(--el-color-primary);
}
.databases-page :deep(.el-input) {
  min-width: 100px;
}
.row-actions {
  flex-wrap: wrap;
}
</style>
