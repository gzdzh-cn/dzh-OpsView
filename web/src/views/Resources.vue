<script setup lang="ts">
import { usePageState } from '../composables/pageState'
import DataList from '../components/DataList.vue'
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Plus,
  Search,
  Connection,
  Coin,
  Box,
  Lock,
  Clock,
  FolderOpened,
} from '@element-plus/icons-vue'
import { usePanel } from '../stores/panel'
import { action, api, errorText, date, post, type Resource } from '../lib/api'
import ResourceForm from '../components/ResourceForm.vue'
import StatusBadge from '../components/StatusBadge.vue'
const props = defineProps<{ kind: string; embedded?: boolean }>(),
  panel = usePanel(),
  dialog = ref(false),
  editing = ref<Resource>(),
  filter = usePageState(() => 'filter:' + props.kind, ''),
  details = ref<Resource>(),
  logs = ref(''),
  logTitle = ref(''),
  showLog = ref(false)
const titles: Record<string, string> = {
  site: '网站',
  database: '数据库',
  instance: '软件实例',
  compose: 'Compose 项目',
  firewall: '防火墙规则',
  backup: '备份任务',
  schedule: '计划任务',
  repository: '插件仓库',
}
const migrationOpen = ref(false),
  migrationSource = ref<Resource>(),
  migrationTarget = ref(''),
  migrating = ref(false),
  certificateOpen = ref(false),
  certificateSource = ref<Resource>(),
  certificateValues = ref({ certificate: '', privateKey: '' })
const redisRestoreOpen = ref(false),
  redisRestoreSource = ref<Resource>(),
  redisRestoreTarget = ref(''),
  redisRestoreConfirmed = ref(false)
const redisRestoreTargets = computed(() =>
  panel
    .kind('instance')
    .filter(
      (r) =>
        r.config.software === 'redis' &&
        r.config.mode === 'native' &&
        r.id !== redisRestoreSource.value?.config.targetId &&
        ['ready', 'running'].includes(r.status),
    ),
)
async function restoreRedis() {
  if (!redisRestoreSource.value || !redisRestoreTarget.value || !redisRestoreConfirmed.value) return
  try {
    await action(redisRestoreSource.value.id, 'restore', {
      targetInstanceId: redisRestoreTarget.value,
      maintenanceConfirmed: true,
    })
    redisRestoreOpen.value = false
    await panel.refresh()
    ElMessage.success('Redis 校验及恢复任务已提交，请在任务中心查看结果')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
const migrationTargets = computed(() =>
  panel
    .kind('instance')
    .filter(
      (r) =>
        r.id !== migrationSource.value?.config.instanceId &&
        r.config.software ===
          panel.resources.find((x) => x.id === migrationSource.value?.config.instanceId)?.config
            .software,
    ),
)
function openCertificate(row: Resource) {
  certificateSource.value = row
  certificateValues.value = { certificate: '', privateKey: '' }
  certificateOpen.value = true
}
function openMigration(row: Resource) {
  migrationSource.value = row
  migrationTarget.value = ''
  migrationOpen.value = true
}
async function migrate() {
  if (!migrationSource.value || !migrationTarget.value) return
  migrating.value = true
  try {
    await ElMessageBox.confirm(
      '请先停止应用写入。将新建目标数据库并验证表和行数；成功后需更新应用连接端口，原库保留。',
      '确认维护窗口',
      { confirmButtonText: '已停止写入，开始迁移', cancelButtonText: '取消' },
    )
    await action(migrationSource.value.id, 'migrate', {
      targetInstanceId: migrationTarget.value,
      maintenanceConfirmed: true,
    })
    migrationOpen.value = false
    await panel.refresh()
    ElMessage.success('迁移任务已提交')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  } finally {
    migrating.value = false
  }
}
async function uploadCertificate() {
  if (!certificateSource.value) return
  try {
    await action(certificateSource.value.id, 'certificate', certificateValues.value)
    certificateOpen.value = false
    await panel.refresh()
    ElMessage.success('证书验证与应用任务已提交')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
function firewallExpired(row: Resource) {
  return row.config.confirmed === false && row.config.confirmDeadline * 1000 < Date.now()
}
const icons: Record<string, any> = {
  site: Connection,
  database: Coin,
  instance: Box,
  compose: Box,
  firewall: Lock,
  backup: FolderOpened,
  schedule: Clock,
}
const rows = computed(() =>
  panel.kind(props.kind).filter((r) => r.name.toLowerCase().includes(filter.value.toLowerCase())),
)
function create() {
  editing.value = undefined
  dialog.value = true
}
function edit(row: Resource) {
  editing.value = row
  dialog.value = true
}
async function execute(row: Resource, op: string, params: Record<string, any> = {}) {
  try {
    if (['delete', 'uninstall'].includes(op)) {
      await ElMessageBox.confirm(
        op === 'delete' && row.kind === 'database'
          ? `将永久删除数据库 ${row.name} 及专用用户。请确认已完成备份。`
          : `确认${op === 'uninstall' ? '卸载' : '移除'} ${row.name}？业务文件、卷和实例数据默认保留。`,
        '确认操作',
        { type: 'warning', confirmButtonText: '确认', cancelButtonText: '取消' },
      )
      params.confirmDataDeletion = true
      if (
        row.status === 'pending' ||
        row.status === 'uninstalled' ||
        ['schedule', 'repository'].includes(row.kind)
      ) {
        await api(`/resources/${row.id}`, { method: 'DELETE' })
        await panel.refresh()
        ElMessage.success(
          row.status === 'uninstalled'
            ? '已清理记录，数据仍保留，可重新安装该版本'
            : '已删除未部署配置',
        )
        return
      }
    }
    if (op === 'restore') {
      const target = panel.resources.find((r) => r.id === row.config.targetId)
      if (target?.kind === 'instance' && target.config.software === 'redis') {
        redisRestoreSource.value = row
        redisRestoreTarget.value = ''
        redisRestoreConfirmed.value = false
        redisRestoreOpen.value = true
        return
      }
      if (target?.kind === 'database') {
        const result = await ElMessageBox.prompt('恢复到新的数据库名称，禁止覆盖源库', '恢复备份', {
          inputPattern: /^[A-Za-z][A-Za-z0-9_]{0,62}$/,
          inputErrorMessage: '请填写有效的新数据库名',
          confirmButtonText: '恢复',
          cancelButtonText: '取消',
        })
        params.databaseName = result.value
      } else
        await ElMessageBox.confirm('恢复到新目录，原站点文件保持不变。', '恢复备份', {
          confirmButtonText: '恢复',
          cancelButtonText: '取消',
        })
    }
    if (op === 'import') {
      const result = await ElMessageBox.prompt(
        '先在文件管理的对应实例目录上传 SQL 文件，然后填写文件名。',
        '导入 SQL',
        { confirmButtonText: '导入', cancelButtonText: '取消', inputPlaceholder: 'backup.sql' },
      )
      params.filename = result.value
    }
    if (op === 'logs') {
      logTitle.value = row.name + ' · 日志'
      showLog.value = true
      logs.value = '正在读取…'
      const data = (await action(row.id, op, params)) as any
      logs.value = data.content || '暂无日志'
      return
    }
    await action(row.id, op, params)
    ElMessage.success('任务已提交，请在任务中心查看进度')
    await panel.refresh()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function configuration(row: Resource) {
  try {
    const result = (await action(row.id, 'configuration')) as any
    logs.value = JSON.stringify(result, null, 2)
    logTitle.value = row.name + ' · 当前配置与历史'
    showLog.value = true
    if (result.drift) {
      await ElMessageBox.confirm(
        '检测到外部修改。配置与历史已显示；确认后将用面板配置重新生成并保留当前文件为历史。',
        '配置漂移',
        { confirmButtonText: '接受差异并重新应用', cancelButtonText: '保留并检查' },
      )
      await execute(row, 'apply', { acceptDrift: true })
    }
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function sync(row: Resource) {
  try {
    await post(`/repositories/${row.id}/refresh`)
    await panel.refresh(['resources', 'tasks', 'catalog'])
    ElMessage.success('仓库已同步')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
</script>
<template>
  <section class="surface">
    <div class="page-toolbar">
      <div class="page-toolbar-left">
        <span class="count-badge">{{ panel.kind(kind).length }}</span>
      </div>
      <div class="page-toolbar-right">
        <el-input
          v-model="filter"
          :prefix-icon="Search"
          placeholder="搜索名称"
          size="small"
          clearable
        />
        <el-button type="primary" :icon="Plus" @click="create">
          {{ kind === 'instance' ? '安装软件' : '添加' + (titles[kind] || '') }}
        </el-button>
      </div>
    </div>
    <DataList
      :key="'Resources-1' + ':' + props.kind"
      :state-key="'Resources-1' + ':' + props.kind"
      v-if="rows.length"
      :rows="rows"
      row-key="id"
    >
      <el-table-column label="名称" min-width="180">
        <template #default="{ row }">
          <div class="resource-identity">
            <span class="resource-avatar"><component :is="icons[kind] || Box" /></span>
            <div>
              <span>{{ row.name }}</span>
              <small class="cell-subtext">
                {{
                  kind === 'site'
                    ? row.config.domains
                    : kind === 'instance'
                      ? `${row.config.software} ${row.config.version}`
                      : row.id.slice(0, 12)
                }}
              </small>
            </div>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="配置" min-width="150">
        <template #default="{ row }">
          <span v-if="kind === 'site'">
            {{ row.config.type === 'static' ? '静态站点' : row.config.upstream }}
          </span>
          <span v-else-if="kind === 'database'">
            {{ row.config.username }}
            <small class="cell-subtext">{{ row.config.source }}</small>
          </span>
          <span v-else-if="kind === 'instance'">
            {{ row.config.mode === 'docker' ? 'Docker' : '原生' }} · :{{ row.config.port }}
          </span>
          <span v-else-if="kind === 'firewall'">
            {{ row.config.action === 'allow' ? '放行' : '阻止' }} {{ row.config.ports }}/{{
              row.config.protocol
            }}
            <small class="cell-subtext">{{ row.config.scope }} · {{ row.config.source }}</small>
          </span>
          <span v-else-if="kind === 'schedule'" class="task-operation">
            {{ row.config.cron }}
            <small class="cell-subtext">
              {{ row.config.enabled ? '已启用' : '已暂停' }} · {{ row.config.operation }}
            </small>
          </span>
          <span v-else-if="kind === 'backup'">
            {{ row.config.destination === 's3' ? 'S3' : '本地加密' }} · 保留
            {{ row.config.retention || 7 }} 份
            <small class="cell-subtext">
              {{ row.config.lastBackupAt ? date(row.config.lastBackupAt) : '尚未备份' }}
            </small>
          </span>
          <span v-else-if="kind === 'repository'">{{ row.config.url }}</span>
          <span v-else>Compose</span>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="105">
        <template #default="{ row }">
          <StatusBadge :value="row.status" />
          <small v-if="kind === 'firewall' && row.config.confirmed === false" class="cell-subtext">
            {{ firewallExpired(row) ? '已超时 · 规则回退' : '待确认 · 超时回退' }}
          </small>
        </template>
      </el-table-column>
      <el-table-column label="操作" min-width="220">
        <template #default="{ row }">
          <div class="row-actions">
            <button @click="details = row">详情</button>
            <template v-if="kind === 'site'">
              <button @click="edit(row)">配置</button>
              <button @click="configuration(row)">配置历史</button>
              <button @click="execute(row, 'renew')">申请 / 续期证书</button>
              <button @click="openCertificate(row)">上传证书</button>
              <button @click="execute(row, 'logs')">日志</button>
            </template>
            <template
              v-if="(kind === 'compose' || kind === 'instance') && row.status !== 'uninstalled'"
            >
              <button v-if="kind !== 'instance'" @click="edit(row)">配置</button>
              <button
                v-if="row.status === 'pending' || (kind !== 'instance' && row.status === 'error')"
                @click="execute(row, kind === 'instance' ? 'install' : 'apply')"
              >
                部署
              </button>
              <button
                v-else-if="row.status !== 'error' || row.config.installedAt"
                @click="execute(row, 'start')"
              >
                启动
              </button>
              <small v-else class="cell-subtext">安装失败，请先卸载并清理记录</small>
              <button @click="execute(row, 'stop')">停止</button>
              <button v-if="kind !== 'instance'" @click="execute(row, 'logs')">日志</button>
            </template>
            <template v-if="kind === 'database'">
              <button @click="edit(row)">访问与密码</button>
              <button v-if="row.status !== 'ready'" @click="execute(row, 'create')">建库</button>
              <button @click="execute(row, 'import')">导入 SQL</button>
              <a
                v-if="row.status === 'ready'"
                :href="'/api/v1/resources/' + row.id + '/download'"
                class="text-link"
              >
                导出
              </a>
              <button v-if="row.status === 'ready'" @click="openMigration(row)">迁移</button>
            </template>
            <template v-if="kind === 'firewall'">
              <button
                v-if="row.config.confirmed === false && !firewallExpired(row)"
                @click="execute(row, 'confirm')"
              >
                确认规则
              </button>
              <button
                v-else-if="
                  row.status === 'pending' || row.status === 'error' || firewallExpired(row)
                "
                @click="execute(row, 'apply')"
              >
                应用
              </button>
            </template>
            <template v-if="kind === 'backup'">
              <button @click="execute(row, 'backup')">立即备份</button>
              <button v-if="row.config.artifact" @click="execute(row, 'restore')">恢复</button>
              <a
                v-if="row.config.artifact"
                :href="'/api/v1/resources/' + row.id + '/download'"
                class="text-link"
              >
                下载
              </a>
              <button @click="edit(row)">配置</button>
            </template>
            <button v-if="kind === 'schedule' || kind === 'repository'" @click="edit(row)">
              编辑
            </button>
            <button v-if="kind === 'repository'" @click="sync(row)">同步</button>
            <button
              class="danger"
              @click="
                () => {
                  execute(
                    row,
                    kind === 'instance' && row.status !== 'uninstalled' ? 'uninstall' : 'delete',
                  )
                }
              "
            >
              {{
                kind === 'instance' ? (row.status === 'uninstalled' ? '清理记录' : '卸载') : '移除'
              }}
            </button>
          </div>
        </template>
      </el-table-column>
    </DataList>
    <div v-else class="empty-page">
      <span class="empty-symbol"><component :is="icons[kind] || Box" /></span>
      <h3>{{ filter ? '没有匹配的资源' : '还没有' + (titles[kind] || '资源') }}</h3>
      <p>
        {{
          kind === 'site'
            ? '先在软件与插件中安装 Nginx，再绑定域名、上传静态文件或配置反向代理。'
            : kind === 'database'
              ? '先安装 MySQL 或 PostgreSQL 实例，再创建独立数据库和专用用户。'
              : kind === 'firewall'
                ? '明确每个开放端口的用途，为宿主机和 Docker 分别配置访问规则。'
                : '创建配置后，通过任务中心追踪实际执行进度。'
        }}
      </p>
      <el-button type="primary" :icon="Plus" @click="create">添加{{ titles[kind] }}</el-button>
    </div>
  </section>
  <ResourceForm v-model="dialog" :kind="kind" :editing="editing" />
  <el-dialog v-model="redisRestoreOpen" title="恢复 Redis 到新实例" width="min(600px,90vw)">
    <p>
      先在软件市场安装另一个 Redis
      实例。目标必须为空且没有关联资源；源实例和持久化文件保留，成功后再切换应用连接。
    </p>
    <el-form label-width="100px">
      <el-form-item label="目标实例">
        <el-select v-model="redisRestoreTarget" placeholder="选择已启动的原生 Redis 空实例">
          <el-option
            v-for="r in redisRestoreTargets"
            :key="r.id"
            :value="r.id"
            :label="`${r.name} · ${r.config.version} · :${r.config.port}`"
          />
        </el-select>
      </el-form-item>
    </el-form>
    <el-checkbox v-model="redisRestoreConfirmed">目标处于维护窗口，没有业务写入</el-checkbox>
    <template #footer>
      <el-button @click="redisRestoreOpen = false">取消</el-button>
      <el-button
        type="primary"
        :disabled="!redisRestoreTarget || !redisRestoreConfirmed"
        @click="restoreRedis"
      >
        校验并恢复
      </el-button>
    </template>
  </el-dialog>
  <el-drawer
    :model-value="!!details"
    :title="details?.name + ' · 资源详情'"
    size="540px"
    @close="details = undefined"
  >
    <template v-if="details">
      <dl class="detail-grid">
        <dt>资源 ID</dt>
        <dd>{{ details.id }}</dd>
        <dt>配置状态</dt>
        <dd><StatusBadge :value="details.status" /></dd>
        <dt>创建时间</dt>
        <dd>{{ date(details.createdAt) }}</dd>
        <dt>配置修订</dt>
        <dd>v{{ details.revision }}</dd>
      </dl>
      <div class="notice">
        “已应用”表示最近一次配置任务成功，实际运行状态请结合服务、端口和日志检查。
      </div>
      <pre class="log-box">{{ JSON.stringify(details.config, null, 2) }}</pre>
    </template>
  </el-drawer>
  <el-dialog v-model="migrationOpen" title="迁移数据库到新实例" width="560px">
    <div class="notice warn">
      迁移前停止应用写入。支持 MySQL 8.4 内迁移、PostgreSQL public schema
      向相同或更高版本迁移；完成后原库仍保留。
    </div>
    <el-form label-position="top">
      <el-form-item label="目标数据库实例">
        <el-select v-model="migrationTarget">
          <el-option
            v-for="r in migrationTargets"
            :key="r.id"
            :label="r.name + ' · ' + r.config.version + ' :' + r.config.port"
            :value="r.id"
          />
        </el-select>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="migrationOpen = false">取消</el-button>
      <el-button type="primary" :disabled="!migrationTarget" :loading="migrating" @click="migrate">
        继续迁移
      </el-button>
    </template>
  </el-dialog>
  <el-dialog v-model="certificateOpen" title="上传站点证书" width="650px">
    <el-form label-position="top">
      <el-form-item label="PEM 证书链">
        <el-input v-model="certificateValues.certificate" type="textarea" :rows="6" />
      </el-form-item>
      <el-form-item label="PEM 私钥">
        <el-input v-model="certificateValues.privateKey" type="textarea" :rows="6" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="certificateOpen = false">取消</el-button>
      <el-button type="primary" @click="uploadCertificate">验证并应用</el-button>
    </template>
  </el-dialog>
  <el-dialog v-model="showLog" :title="logTitle" width="800px">
    <pre class="log-box">{{ logs }}</pre>
  </el-dialog>
</template>
