<script setup lang="ts">
import DialogActions from './DialogActions.vue'
import AppDialog from './AppDialog.vue'
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import DataList from './DataList.vue'
import { usePageState } from '../composables/pageState'
import LoadingContent from './LoadingContent.vue'
import { usePageQuery } from '../composables/pageQuery'
import { useSecurityActions } from '../composables/securityActions'
import { post, errorText, type Config } from '../lib/api'
const { data, busy: reading, error, load, hasData } = usePageQuery<Config>('/system/firewall', {})
const { busy, taskId, run } = useSecurityActions(() => load(true), '/system/firewall/')
const { busy: modeBusy, taskId: modeTaskId, run: modeRun } = useSecurityActions(() => load(true))
const tab = usePageState('firewall-category', 'port')
const actionLabels: Record<string, string> = {
  allow: '放行',
  deny: '阻止',
  reject: '拒绝',
  limit: '限速',
}
const kinds: Record<string, string> = {
  port: '端口规则',
  ip: 'IP规则',
  forward: '端口转发',
  region: '地区规则',
}
const rows = computed<Config[]>(() =>
  (data.value.rules || []).filter((r: Config) => r.kind === tab.value),
)
const count = (kind: string) =>
  data.value.counts?.[kind] ??
  (data.value.rules || []).filter((r: Config) => r.kind === kind).length
const locked = computed(
  () =>
    !data.value.compatible || busy.value || modeBusy.value || Boolean(data.value.pending?.deadline),
)
const modeLocked = computed(() => locked.value || data.value.modeAvailable === false)
const selected = ref<Config[]>([]),
  editOpen = ref(false),
  editing = ref(''),
  form = ref<Config>({})
const source = ref(''),
  acknowledged = ref(false),
  modeOpen = ref(false)
function prepareManagementSource() {
  if (!source.value) source.value = data.value.managementSource || ''
}
function openMode() {
  prepareManagementSource()
  acknowledged.value = false
  modeOpen.value = true
}
function openImport() {
  preview.value = {}
  importOpen.value = true
}
function openEdit(row?: Config) {
  prepareManagementSource()
  editing.value = row?.id || ''
  form.value = row
    ? { ...row, countries: [...(row.countries || [])] }
    : {
        kind: tab.value,
        protocol: tab.value === 'ip' || tab.value === 'region' ? 'any' : 'tcp',
        ports: '',
        source: 'any',
        destination: 'any',
        action: 'allow',
        direction: 'in',
        family: 'ipv4',
        zone: data.value.defaultZone || '',
        note: '',
        target: '',
        targetPorts: '',
        masquerade: false,
        countries: [],
      }
  if (form.value.protocol === 'service') {
    form.value.protocol = 'tcp'
    form.value.ports = ''
  }
  acknowledged.value = false
  editOpen.value = true
}
const conflictingRules = computed(() =>
  (data.value.rules || [])
    .filter((row: Config) => {
      if (
        row.id === editing.value ||
        row.direction !== form.value.direction ||
        row.action === form.value.action
      )
        return false
      if (row.family !== 'dual' && form.value.family !== 'dual' && row.family !== form.value.family)
        return false
      if (
        !['any', 'tcp+udp', form.value.protocol].includes(row.protocol) &&
        !['any', 'tcp+udp'].includes(form.value.protocol)
      )
        return false
      const spans = (value: string) =>
        (value || '1-65535')
          .replaceAll(':', '-')
          .split(',')
          .map((v) => {
            const [a, b] = v.split('-').map(Number)
            return [a, b ?? a]
          })
      return spans(row.ports).some(([a, b]) =>
        spans(form.value.ports).some(([c, d]) => a! <= d! && c! <= b!),
      )
    })
    .slice(0, 8),
)
const safeParams = () => ({
  revision: data.value.revision,
  source: source.value,
  confirmedImpact: acknowledged.value,
})
async function save() {
  if (await run('save', { ...safeParams(), id: editing.value, rule: form.value }))
    editOpen.value = false
}
const deleteOpen = ref(false),
  deleteTargets = ref<Config[]>([])
const deleteNeedsProtection = computed(() =>
  deleteTargets.value.some((row) => {
    if (row.direction !== 'in' || (row.action !== 'allow' && !row.service)) return false
    if (row.protocol === 'service' && row.service) return true
    return (data.value.protectedPorts || []).some(
      (port: number) =>
        !row.ports ||
        row.ports
          .replaceAll(':', '-')
          .split(',')
          .some((span: string) => {
            const [a, b] = span.split('-').map(Number)
            return port >= a! && port <= (b ?? a)!
          }),
    )
  }),
)
function remove(row: Config) {
  openDelete([row])
}
function removeSelected() {
  openDelete([...selected.value])
}
function openDelete(targets: Config[]) {
  prepareManagementSource()
  acknowledged.value = false
  deleteTargets.value = targets
  deleteOpen.value = true
}
async function applyDelete() {
  for (const row of [...deleteTargets.value]) {
    if (!(await run('delete', { ...safeParams(), id: row.id }))) return
    deleteTargets.value = deleteTargets.value.filter((target) => target.id !== row.id)
    if (data.value.pending?.deadline) {
      deleteOpen.value = false
      return
    }
  }
  deleteOpen.value = false
}
async function confirmPending() {
  try {
    await ElMessageBox.confirm(
      '请重新打开面板并验证 SSH 新连接。确认后保留本次修改。',
      '确认访问正常',
      { confirmButtonText: '访问正常，保留' },
    )
    if (data.value.pending?.mode) await modeRun('firewall-confirm', { connected: true }, false)
    else await run('confirm', { connected: true }, false)
  } catch {
    /* cancelled */
  }
}
async function changeMode() {
  if (
    await modeRun('firewall-mode', {
      enabled: !data.value.enabled,
      source: source.value,
      confirmedImpact: acknowledged.value,
    })
  )
    modeOpen.value = false
}
async function changePing() {
  try {
    await ElMessageBox.confirm(
      data.value.pingBlocked
        ? '恢复响应 Ping？'
        : '仅阻止 Echo Request，保留必要 ICMP 通信。确认禁 Ping？',
      '禁 Ping',
    )
    await run('ping', { enabled: !data.value.pingBlocked })
  } catch {
    /* cancelled */
  }
}
const importOpen = ref(false),
  importContent = ref(''),
  preview = ref<Config>({}),
  previewSelection = ref<Config[]>([]),
  previewBusy = ref(false)
async function readImport(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  if (file.size > 10 * 1024 * 1024) {
    ElMessage.error('规则文件不能超过 10 MiB')
    return
  }
  importContent.value = await file.text()
  preview.value = {}
}
async function previewImport() {
  previewBusy.value = true
  try {
    preview.value = await post<Config>('/system/firewall/import-preview', {
      content: importContent.value,
      kind: tab.value,
    })
    previewSelection.value = []
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    previewBusy.value = false
  }
}
async function applyImport() {
  if (
    await run('import-apply', {
      ...safeParams(),
      token: preview.value.token,
      indices: previewSelection.value.map((r) => r.index),
    })
  )
    importOpen.value = false
}
async function exportRules(scope: string = 'current') {
  try {
    const result = await post<Config>('/system/firewall/export', {
      kind: scope === 'all' ? '' : tab.value,
      ids: scope === 'all' ? [] : selected.value.map((r) => r.id),
    })
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(result, null, 2)], { type: 'application/json' }),
    )
    const link = document.createElement('a')
    link.href = url
    link.download = `opsview-firewall-${scope === 'all' ? 'all' : tab.value}.json`
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
const syncOpen = ref(false),
  sync = ref<Config>({}),
  syncSelected = ref<Config[]>([])
async function openSync() {
  try {
    sync.value = await post<Config>('/system/firewall/sync-preview', {})
    syncSelected.value = []
    syncOpen.value = true
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function observe() {
  if (
    await run('sync-observe', {
      revision: sync.value.revision,
      ids: syncSelected.value.map((r) => r.rule.id),
    })
  )
    syncOpen.value = false
}
async function restore() {
  try {
    await ElMessageBox.confirm(
      '将明确勾选的历史缺失规则重新写入系统，可能改变当前访问策略。',
      '恢复历史规则',
      { type: 'warning' },
    )
    if (
      await run('sync-restore', {
        ...safeParams(),
        indices: syncSelected.value
          .filter((r) => r.change === 'system-missing')
          .map((r) => r.rule.id),
      })
    )
      syncOpen.value = false
  } catch {
    /* cancelled */
  }
}
const protectionOpen = ref(false),
  protection = ref<Config>({}),
  protectionConfig = ref<Config>({}),
  whitelist = ref(''),
  unbanIP = ref(''),
  jail = ref('opsview-sshd')
async function openProtection() {
  try {
    protection.value = await post<Config>('/system/firewall/protection-status', {})
    protectionConfig.value = { ...protection.value.config }
    whitelist.value = (protectionConfig.value.whitelist || []).join('\n')
    protectionOpen.value = true
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function saveProtection() {
  if (
    await run('protection-save', {
      config: {
        ...protectionConfig.value,
        whitelist: whitelist.value.split(/[\s,]+/).filter(Boolean),
      },
    })
  )
    await openProtection()
}
const countryCodes =
  'AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW'.split(
    ' ',
  )
const countryNames = new Intl.DisplayNames(['zh-CN'], { type: 'region' })
async function installDependencies() {
  try {
    await ElMessageBox.confirm('安装地区地址集合所需 ipset，操作进入任务中心。', '安装地区依赖')
    await run('dependencies', { confirmedImpact: true })
  } catch {
    /* cancelled */
  }
}
function rowName(row: Config) {
  return row.ports || row.source || row.countries?.join('、') || '规则'
}
defineExpose({ openProtection })
</script>
<template>
  <section class="surface security-panel firewall-panel" aria-label="系统防火墙">
    <div class="page-toolbar">
      <div class="page-toolbar-left firewall-switches">
        <span>防火墙</span>
        <el-switch
          :model-value="Boolean(data.enabled)"
          :loading="reading"
          :disabled="modeLocked"
          aria-label="防火墙开关"
          @change="openMode"
        />
        <span>禁 Ping</span>
        <el-switch
          :model-value="Boolean(data.pingBlocked)"
          :loading="reading"
          :disabled="locked"
          aria-label="禁 Ping"
          @change="changePing"
        />
        <el-tag v-if="data.backend" size="small" type="info">{{ data.backend }}</el-tag>
        <el-tag v-if="data.installed && !data.enabled" type="warning">规则未生效</el-tag>
      </div>
      <el-popover placement="bottom-end" width="560" trigger="click">
        <template #reference><el-button text aria-label="防火墙诊断">详情</el-button></template>
        <p>{{ data.externalRestrictions }}</p>
        <p>保护端口：{{ data.protectedPorts?.join('、') || data.portReason || '不可用' }}</p>
        <pre class="log-box">{{ data.raw }}</pre>
        <el-collapse>
          <el-collapse-item title="Docker 独立入站规则">
            <pre class="log-box">{{ data.dockerRules }}{{ data.dockerIPv6Rules }}</pre>
          </el-collapse-item>
        </el-collapse>
      </el-popover>
    </div>
    <el-alert
      v-if="error || data.reason"
      :title="error || data.reason"
      type="warning"
      :closable="false"
    >
      <el-button v-if="error" text @click="load(true)">重试</el-button>
    </el-alert>
    <el-alert
      v-if="data.warning || data.modeReason"
      :title="[data.warning, data.modeReason].filter(Boolean).join(' ')"
      type="info"
      :closable="false"
    />
    <el-alert
      v-if="data.pending?.deadline"
      type="warning"
      :closable="false"
      class="security-pending"
    >
      <template #title>
        修改待确认 · {{ new Date(data.pending.deadline * 1000).toLocaleTimeString() }} 超时回退
      </template>
      <p v-if="data.pending.rollbackError">回退待重试：{{ data.pending.rollbackError }}</p>
      <el-button type="primary" :disabled="busy || modeBusy" @click="confirmPending">
        访问正常，确认保留
      </el-button>
      <el-button
        :disabled="busy || modeBusy"
        @click="
          data.pending.mode
            ? modeRun('firewall-rollback', {}, false)
            : run(data.pending.feature ? 'rollback-feature' : 'rollback', {}, false)
        "
      >
        立即回退
      </el-button>
    </el-alert>
    <router-link v-if="taskId || modeTaskId" to="/tasks">操作进行中，查看任务日志</router-link>
    <el-tabs v-model="tab" @tab-change="selected = []">
      <el-tab-pane
        v-for="(label, kind) in kinds"
        :key="kind"
        :name="kind"
        :label="`${label}：${hasData ? count(kind) : '—'}`"
      />
    </el-tabs>
    <div class="page-toolbar">
      <div class="page-toolbar-left">
        <el-button type="primary" :disabled="locked" @click="openEdit()">
          添加{{ kinds[tab] }}
        </el-button>
        <el-button :disabled="locked" @click="openImport">导入规则</el-button>
        <el-dropdown
          split-button
          :disabled="!data.installed"
          @click="exportRules()"
          @command="exportRules"
        >
          导出规则
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="current">
                {{ selected.length ? '导出所选规则' : '导出当前类别' }}
              </el-dropdown-item>
              <el-dropdown-item command="all">导出全部类别</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-button :disabled="locked" @click="openProtection">端口防扫描</el-button>
        <el-button :disabled="locked" @click="openSync">同步规则</el-button>
        <el-button v-if="tab === 'region'" :disabled="locked" @click="run('region-update')">
          更新地区数据
        </el-button>
        <el-button
          v-if="tab === 'region' && data.backend === 'ufw'"
          :disabled="locked"
          @click="installDependencies"
        >
          安装地区依赖
        </el-button>
        <el-button v-if="selected.length" type="danger" :disabled="locked" @click="removeSelected">
          删除所选 ({{ selected.length }})
        </el-button>
      </div>
    </div>
    <LoadingContent :busy="reading" :ready="hasData">
      <DataList
        :key="tab"
        :rows="rows"
        searchable
        :state-key="`firewall-${tab}`"
        row-key="id"
        @selection-change="selected = $event"
      >
        <el-table-column
          type="selection"
          width="42"
          :selectable="(row: Config) => row.editable && !locked"
        />
        <el-table-column
          v-if="tab === 'port' || tab === 'forward'"
          prop="protocol"
          label="协议"
          width="85"
        />
        <el-table-column
          v-if="tab === 'port' || tab === 'forward'"
          :label="tab === 'forward' ? '入口端口' : '端口'"
          min-width="130"
        >
          <template #default="{ row }">
            {{ row.ports }}
            <el-tag size="small" type="info">
              {{ row.family === 'ipv6' ? 'IPv6' : row.family === 'dual' ? '双栈' : 'IPv4' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="tab === 'port'" label="监听状态" width="105">
          <template #default="{ row }">
            <el-tag :type="row.listening ? 'success' : 'info'" size="small">
              {{ row.listening ? '已监听' : '未监听' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="tab === 'ip'" label="IP/CIDR" min-width="190" show-overflow-tooltip>
          <template #default="{ row }">
            {{ row.direction === 'out' ? row.destination : row.source }}
          </template>
        </el-table-column>
        <el-table-column v-if="tab === 'region'" label="国家/地区" min-width="180">
          <template #default="{ row }">
            {{ row.countries?.map((code: string) => countryNames.of(code) || code).join('、') }}
          </template>
        </el-table-column>
        <el-table-column v-if="tab === 'region'" label="作用端口" min-width="120">
          <template #default="{ row }">{{ row.ports || '全部端口' }}</template>
        </el-table-column>
        <el-table-column v-if="tab !== 'forward'" label="策略" width="80">
          <template #default="{ row }">
            <el-tag :type="row.action === 'allow' ? 'success' : 'danger'" size="small">
              {{ actionLabels[row.action] || row.action }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="tab === 'port' || tab === 'ip'" label="方向" width="80">
          <template #default="{ row }">{{ row.direction === 'out' ? '出站' : '入站' }}</template>
        </el-table-column>
        <el-table-column
          v-if="tab === 'port' || tab === 'forward'"
          label="来源 / 目标"
          min-width="170"
          show-overflow-tooltip
        >
          <template #default="{ row }">
            {{ row.source === 'any' ? '所有 IP' : row.source
            }}{{ row.destination !== 'any' ? ` → ${row.destination}` : '' }}
          </template>
        </el-table-column>
        <el-table-column v-if="tab === 'forward'" label="目标地址" min-width="150">
          <template #default="{ row }">{{ row.target || '本机' }}</template>
        </el-table-column>
        <el-table-column v-if="tab === 'forward'" prop="targetPorts" label="目标端口" width="120" />
        <el-table-column v-if="data.backend === 'firewalld'" prop="zone" label="区域" width="100" />
        <el-table-column prop="note" label="备注" min-width="140" show-overflow-tooltip />
        <el-table-column v-if="tab === 'region'" label="数据更新时间" min-width="170">
          <template #default="{ row }">
            {{ row.dataUpdatedAt ? new Date(row.dataUpdatedAt).toLocaleString() : '—' }}
          </template>
        </el-table-column>
        <el-table-column v-else label="时间" min-width="160">
          <template #default="{ row }">
            {{ row.createdAt ? new Date(row.createdAt).toLocaleString() : '—' }}
          </template>
        </el-table-column>
        <el-table-column type="expand" width="32">
          <template #default="{ row }">
            <p>{{ row.reason || '原始系统规则' }}</p>
            <pre class="log-box">{{ row.raw || 'OpsView 受管扩展' }}</pre>
            <small>
              运行：{{ row.runtime ? '存在' : '未生效' }} · 持久化：{{
                row.permanent ? '已保存' : '未保存'
              }}
            </small>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="145" fixed="right">
          <template #default="{ row }">
            <el-tooltip
              v-if="!data.enabled || row.runtime === false || row.permanent === false"
              :content="
                !data.enabled || row.runtime === false
                  ? '保存规则未生效'
                  : '仅运行配置，重载后可能丢失'
              "
            >
              <el-tag size="small" type="warning">
                {{ !data.enabled || row.runtime === false ? '未生效' : '临时' }}
              </el-tag>
            </el-tooltip>
            <el-tooltip :content="row.reason || '修改规则'" :disabled="row.editable">
              <span>
                <el-button
                  link
                  type="primary"
                  :disabled="locked || !row.editable"
                  @click="openEdit(row)"
                >
                  修改
                </el-button>
              </span>
            </el-tooltip>
            <el-button link type="danger" :disabled="locked || !row.editable" @click="remove(row)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </DataList>
    </LoadingContent>
    <details v-if="hasData" class="firewall-access">
      <summary>管理来源保护</summary>
      <el-input
        v-model="source"
        placeholder="涉及 SSH/面板端口时填写客户端 IP/CIDR"
        aria-label="管理来源"
      />
      <el-checkbox v-model="acknowledged">已确认影响并保留 SSH 会话</el-checkbox>
    </details>
  </section>
  <AppDialog
    size="small"
    v-model="deleteOpen"
    title="删除防火墙规则"
    width="560px"
    :close-on-click-modal="false"
  >
    <p>将从系统防火墙删除以下 {{ deleteTargets.length }} 条规则，修改同时保存到系统配置。</p>
    <p v-for="row in deleteTargets" :key="row.id">
      {{ rowName(row) }} · {{ row.family }} · {{ actionLabels[row.action] || row.action }}
    </p>
    <el-alert
      v-if="deleteNeedsProtection"
      title="涉及 SSH 或面板访问。会临时保留管理来源通道，120 秒未确认自动回退。"
      type="warning"
      :closable="false"
    />
    <el-form v-if="deleteNeedsProtection" label-width="95px" class="security-form">
      <el-form-item label="管理来源">
        <el-input v-model="source" placeholder="实际客户端 IP/CIDR" aria-label="删除管理来源" />
      </el-form-item>
      <el-form-item>
        <el-checkbox v-model="acknowledged">已确认影响并保留 SSH 会话</el-checkbox>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button :disabled="busy" @click="deleteOpen = false">取消</el-button>
      <el-button
        type="danger"
        :loading="busy"
        :disabled="!deleteTargets.length || (deleteNeedsProtection && (!acknowledged || !source))"
        @click="applyDelete"
      >
        删除
      </el-button>
    </template>
  </AppDialog>
  <AppDialog
    v-model="editOpen"
    :title="`${editing ? '修改' : '添加'}${kinds[form.kind]}`"
    width="620px"
    :close-on-click-modal="false"
  >
    <el-form label-width="105px">
      <el-form-item label="地址族">
        <el-radio-group v-model="form.family">
          <el-radio value="ipv4">IPv4</el-radio>
          <el-radio value="ipv6">IPv6</el-radio>
          <el-radio v-if="data.backend === 'firewalld'" value="dual">IPv4 + IPv6</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="data.backend === 'firewalld'" label="区域">
        <el-select v-model="form.zone">
          <el-option v-for="zone in data.zones || []" :key="zone" :value="zone" />
        </el-select>
      </el-form-item>
      <el-form-item label="协议">
        <el-select v-model="form.protocol">
          <el-option label="TCP" value="tcp" />
          <el-option label="UDP" value="udp" />
          <el-option v-if="form.kind !== 'region'" label="TCP + UDP" value="tcp+udp" />
          <el-option
            v-if="form.kind === 'ip' || form.kind === 'region'"
            label="所有协议"
            value="any"
          />
        </el-select>
      </el-form-item>
      <el-form-item
        v-if="form.kind !== 'ip'"
        :label="form.kind === 'forward' ? '入口端口' : '端口'"
      >
        <el-input
          v-model="form.ports"
          :placeholder="form.kind === 'region' ? '留空为全部端口' : '例如 80,443,8000-8010'"
          aria-label="规则端口"
        />
      </el-form-item>
      <el-form-item v-if="form.kind === 'region'" label="国家/地区">
        <el-select v-model="form.countries" multiple filterable>
          <el-option
            v-for="code in countryCodes"
            :key="code"
            :value="code"
            :label="countryNames.of(code) || code"
          />
        </el-select>
      </el-form-item>
      <el-form-item
        v-if="form.kind !== 'region'"
        :label="form.direction === 'out' ? '来源地址' : '来源 IP'"
      >
        <el-input v-model="form.source" placeholder="any 或 IP/CIDR" aria-label="规则来源" />
      </el-form-item>
      <el-form-item v-if="form.kind !== 'region'" label="目标地址">
        <el-input v-model="form.destination" placeholder="any 或 IP/CIDR" />
      </el-form-item>
      <template v-if="form.kind === 'forward'">
        <el-form-item label="转发到">
          <el-input v-model="form.target" placeholder="留空为本机；远程填写同地址族单个 IP" />
        </el-form-item>
        <el-form-item label="目标端口">
          <el-input v-model="form.targetPorts" placeholder="单端口或等长范围" />
        </el-form-item>
        <el-form-item label="源地址转换">
          <el-checkbox v-model="form.masquerade" :disabled="!form.target">
            启用 MASQUERADE
          </el-checkbox>
        </el-form-item>
        <el-alert
          title="远程转发需要开启系统 IP forwarding 并允许路由；源地址转换会隐藏客户端 IP。"
          type="warning"
          :closable="false"
        />
      </template>
      <el-form-item v-if="form.kind !== 'forward'" label="策略">
        <el-radio-group v-model="form.action">
          <el-radio value="allow">放行</el-radio>
          <el-radio value="deny">阻止</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="form.kind === 'port' || form.kind === 'ip'" label="方向">
        <el-radio-group v-model="form.direction">
          <el-radio value="in">入站</el-radio>
          <el-radio value="out">出站</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-alert
        v-if="form.kind === 'region'"
        :title="
          data.backend === 'ufw'
            ? '地区放行仅允许所选地区。管理来源例外先处理，随后按地区规则保存顺序匹配，再进入系统规则。'
            : '地区放行不会阻止其他地区。管理来源例外优先；默认 rich 规则中阻止先于放行。'
        "
        type="info"
        :closable="false"
      />
      <el-alert
        v-if="conflictingRules.length"
        :title="`可能重叠的相反策略：${conflictingRules.map((r: Config) => `${r.ports || '全部端口'} / ${r.source}`).join('；')}。请核对原规则及优先关系。`"
        type="warning"
        :closable="false"
      />
      <el-form-item label="备注"><el-input v-model="form.note" maxlength="256" /></el-form-item>
      <el-form-item label="管理来源">
        <el-input v-model="source" placeholder="涉及管理访问时必填 IP/CIDR" />
      </el-form-item>
      <el-form-item>
        <el-checkbox v-model="acknowledged">已确认影响并保留 SSH 会话</el-checkbox>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="editOpen = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="save">保存</el-button>
    </template>
  </AppDialog>
  <AppDialog
    size="small"
    v-model="modeOpen"
    :title="data.enabled ? '关闭防火墙' : '启用防火墙'"
    width="560px"
    :close-on-click-modal="false"
  >
    <el-alert
      title="保留现有规则。请同时检查云安全组，120 秒未确认会自动恢复原状态。"
      type="warning"
      :closable="false"
    />
    <el-form label-width="95px" class="security-form">
      <el-form-item v-if="!data.enabled" label="放行来源">
        <el-input v-model="source" placeholder="面板和 SSH 客户端 IP/CIDR" />
      </el-form-item>
      <el-form-item>
        <el-checkbox v-model="acknowledged">已了解影响，并保留现有 SSH 会话</el-checkbox>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="modeOpen = false">取消</el-button>
      <el-button
        type="primary"
        :loading="modeBusy"
        :disabled="!acknowledged || (!data.enabled && !source)"
        @click="changeMode"
      >
        应用并等待确认
      </el-button>
    </template>
  </AppDialog>
  <AppDialog v-model="importOpen" title="导入防火墙规则" width="850px">
    <el-alert
      title="支持 OpsView JSON、宝塔 JSON 及分隔记录。先预览，再明确勾选可应用项。"
      :closable="false"
      type="info"
    />
    <input type="file" accept=".json,.txt,.csv" aria-label="选择规则文件" @change="readImport" />
    <el-input
      v-model="importContent"
      type="textarea"
      :rows="5"
      placeholder="粘贴规则内容"
      @input="preview = {}"
    />
    <el-button :loading="previewBusy" @click="previewImport">预览规则</el-button>
    <el-table
      v-if="preview.rows"
      :data="preview.rows"
      @selection-change="previewSelection = $event"
    >
      <el-table-column type="selection" :selectable="(row: Config) => row.status === 'ready'" />
      <el-table-column label="规则">
        <template #default="{ row }">
          {{ row.rule.kind }} {{ rowName(row.rule) }} {{ row.rule.family }}
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="110" />
      <el-table-column prop="reason" label="说明" />
    </el-table>
    <el-input v-model="source" placeholder="涉及管理访问时填写客户端 IP/CIDR" />
    <el-checkbox v-model="acknowledged">已确认影响</el-checkbox>
    <template #footer>
      <el-button @click="importOpen = false">取消</el-button>
      <el-button
        type="primary"
        :disabled="!previewSelection.length"
        :loading="busy"
        @click="applyImport"
      >
        应用所选规则
      </el-button>
    </template>
  </AppDialog>
  <AppDialog v-model="syncOpen" title="同步规则差异" width="850px">
    <el-alert
      title="默认以系统实际配置更新记录。恢复历史规则会写入系统，需要单独选择并确认。"
      type="info"
      :closable="false"
    />
    <el-table :data="sync.rows || []" @selection-change="syncSelected = $event">
      <el-table-column type="selection" />
      <el-table-column label="规则">
        <template #default="{ row }">{{ rowName(row.rule) }} {{ row.rule.family }}</template>
      </el-table-column>
      <el-table-column label="变化">
        <template #default="{ row }">
          {{ row.change === 'system-added' ? '系统新增' : '系统已移除' }}
        </template>
      </el-table-column>
    </el-table>
    <el-input v-model="source" placeholder="恢复涉及管理端口的规则时填写客户端 IP/CIDR" />
    <el-checkbox v-model="acknowledged">已确认恢复影响</el-checkbox>
    <template #footer>
      <el-button @click="syncOpen = false">取消</el-button>
      <el-button
        :disabled="!syncSelected.some((r) => r.change === 'system-missing')"
        :loading="busy"
        @click="restore"
      >
        恢复所选历史规则
      </el-button>
      <el-button type="primary" :loading="busy" @click="observe">以系统更新记录</el-button>
    </template>
  </AppDialog>
  <AppDialog v-model="protectionOpen" title="端口防扫描 · 服务异常访问防护" width="720px">
    <el-alert
      :title="protection.notice || '基于 Fail2ban 日志的防护'"
      type="info"
      :closable="false"
    />
    <DialogActions>
      <el-button
        v-if="!protection.installed"
        :loading="busy"
        @click="run('protection-install').then(openProtection)"
      >
        安装 Fail2ban
      </el-button>
    </DialogActions>
    <el-form v-if="protection.installed" label-width="110px">
      <el-form-item label="SSH 防护">
        <el-switch v-model="protectionConfig.ssh" :disabled="!protection.sshAvailable" />
      </el-form-item>
      <el-form-item label="Nginx 认证防护">
        <el-switch v-model="protectionConfig.nginx" :disabled="!protection.nginxAvailable" />
      </el-form-item>
      <el-form-item label="失败次数">
        <el-input-number v-model="protectionConfig.maxRetry" :min="1" :max="1000" />
      </el-form-item>
      <el-form-item label="统计窗口(秒)">
        <el-input-number v-model="protectionConfig.findTime" :min="10" :max="86400" />
      </el-form-item>
      <el-form-item label="封禁时间(秒)">
        <el-input-number v-model="protectionConfig.banTime" :min="10" :max="604800" />
      </el-form-item>
      <el-form-item label="管理白名单">
        <el-input
          v-model="whitelist"
          type="textarea"
          placeholder="每行一个 IP/CIDR，必须包含管理来源"
        />
      </el-form-item>
      <DialogActions>
        <el-button type="primary" :loading="busy" @click="saveProtection">保存防护设置</el-button>
      </DialogActions>
    </el-form>
    <el-collapse>
      <el-collapse-item v-for="item in protection.jails || []" :key="item.name" :title="item.name">
        <pre class="log-box">{{ item.status }}{{ item.bans }}</pre>
      </el-collapse-item>
    </el-collapse>
    <div class="page-toolbar">
      <el-select v-model="jail">
        <el-option value="opsview-sshd" />
        <el-option value="opsview-nginx-http-auth" />
      </el-select>
      <el-input v-model="unbanIP" placeholder="需要解封的 IP" />
      <el-button
        :disabled="!unbanIP || busy"
        @click="run('protection-unban', { jail, ip: unbanIP }).then(openProtection)"
      >
        解封
      </el-button>
    </div>
  </AppDialog>
</template>
<style scoped>
.firewall-switches {
  gap: 10px;
}
.firewall-access {
  padding: 8px 0;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.firewall-access .el-input {
  max-width: 360px;
  margin: 8px;
}
.firewall-panel :deep(.el-tabs__header) {
  margin-bottom: 12px;
}
</style>
