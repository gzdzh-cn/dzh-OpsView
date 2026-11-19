<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { usePageQuery } from '../composables/pageQuery'
import { usePanel } from '../stores/panel'
import { api, post, errorText, date, type Config, type Resource } from '../lib/api'
import AppDialog from './AppDialog.vue'
import DataList from './DataList.vue'
import LoadingContent from './LoadingContent.vue'
const tab = ref('rules'),
  panel = usePanel()
const open = ref(false)
const query = usePageQuery<Resource[]>(() => tab.value === 'rules' ? '/notifications/rules' : '', [])
const channels = usePageQuery<Resource[]>(() => tab.value === 'channels' || open.value && tab.value === 'rules' ? '/notifications/channels' : '', [])
const records = usePageQuery<Config[]>(() => tab.value === 'records' ? '/notifications/records' : '', [])
const active = computed(() =>
  tab.value === 'rules' ? query : tab.value === 'channels' ? channels : records,
)
const types: Record<string, string> = {
  cpu: 'CPU 使用率',
  memory: '内存使用率',
  disk: '磁盘使用率',
  inode: 'inode 使用率',
  service: '受管服务异常',
  website: '网站不可用',
  certificate: '证书到期',
  'task-failed': '任务失败',
  'backup-failed': '备份失败',
  'login-failed': '异常登录',
}
const providers: Record<string, string> = {
  smtp: 'SMTP 邮件',
  dingtalk: '钉钉',
  feishu: '飞书',
  wecom: '企业微信',
  webhook: '通用 Webhook',
}
const editing = ref<Resource>(),
  name = ref(''),
  config = ref<Config>({}),
  busy = ref(false),
  error = ref('')
const targets = computed(() =>
  panel.resources.filter((r) =>
    config.value.type === 'service' ? r.kind === 'instance' : r.kind === 'site',
  ),
)
function edit(row?: Resource) {
  editing.value = row
  name.value = row?.name || ''
  config.value = row
    ? { ...row.config }
    : {
        type: tab.value === 'rules' ? 'cpu' : 'smtp',
        enabled: true,
        threshold: 80,
        samples: 3,
        cooldownMinutes: 15,
        recovery: true,
        channels: [],
        port: 465,
        quietStart: '',
        quietEnd: '',
      }
  error.value = ''
  open.value = true
}
watch(open, value => { if (!value) { config.value = {}; error.value = '' } })
async function save() {
  busy.value = true
  try {
    await api('/notifications/' + tab.value + (editing.value ? '/' + editing.value.id : ''), {
      method: editing.value ? 'PUT' : 'POST',
      body: JSON.stringify({
        name: name.value,
        config: config.value,
        revision: editing.value?.revision,
      }),
    })
    open.value = false
    await active.value.load(true)
    ElMessage.success('已保存')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function remove(row: Resource) {
  try {
    await ElMessageBox.confirm('删除 ' + row.name + '？', '删除配置', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
    await api('/notifications/' + tab.value + '/' + row.id, { method: 'DELETE' })
    await active.value.load(true)
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function test(row: Resource) {
  try {
    await post('/notifications/channels/' + row.id + '/test')
    ElMessage.success('测试通知已发送')
    if (tab.value === 'records') void records.load(true)
  } catch (e) {
    ElMessage.error(errorText(e))
    if (tab.value === 'records') void records.load(true)
  }
}
</script>
<template>
  <div class="surface">
    <el-tabs v-model="tab" class="panel-tabs">
      <el-tab-pane label="规则" name="rules" />
      <el-tab-pane label="通知渠道" name="channels" />
      <el-tab-pane label="发送记录" name="records" />
    </el-tabs>
    <div class="page-toolbar">
      <el-button v-if="tab !== 'records'" type="primary" @click="edit()">
        {{ tab === 'rules' ? '添加告警规则' : '添加通知渠道' }}
      </el-button>
      <el-button v-else @click="records.load(true)">重新加载记录</el-button>
    </div>
    <el-alert v-if="active.error.value" :title="active.error.value" type="error" :closable="false">
      <el-button link @click="active.load(true)">重试</el-button>
    </el-alert>
    <LoadingContent :busy="active.busy.value">
      <DataList
        v-if="tab !== 'records'"
        :rows="active.data.value"
        :state-key="'notification-' + tab"
        searchable
      >
        <el-table-column prop="name" label="名称" min-width="180" />
        <el-table-column label="类型" min-width="150">
          <template #default="{ row }">
            {{ (tab === 'rules' ? types : providers)[row.config.type] }}
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.config.enabled ? 'success' : 'info'">
              {{ row.config.enabled ? '已启用' : '已暂停' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column v-if="tab === 'rules'" label="阈值 / 冷却" min-width="180">
          <template #default="{ row }">
            {{ row.config.threshold }} / {{ row.config.cooldownMinutes }} 分钟
          </template>
        </el-table-column>
        <el-table-column label="操作" width="200">
          <template #default="{ row }">
            <div class="row-actions">
              <button @click="edit(row)">编辑</button>
              <button v-if="tab === 'channels'" @click="test(row)">测试</button>
              <button class="danger" @click="remove(row)">删除</button>
            </div>
          </template>
        </el-table-column>
      </DataList>
      <DataList v-else :rows="records.data.value" state-key="notification-records" searchable>
        <el-table-column prop="title" label="摘要" min-width="180" />
        <el-table-column label="结果" width="100"><template #default="{row}">{{ ({queued:'待发送',succeeded:'已发送',failed:'失败'} as Record<string,string>)[row.state] || row.state }}</template></el-table-column>
        <el-table-column label="渠道" min-width="130">
          <template #default="{ row }">
            {{ channels.data.value.find((c) => c.id === row.channelId)?.name || row.channelId }}
          </template>
        </el-table-column>
        <el-table-column prop="attempts" label="尝试次数" width="100" />
        <el-table-column prop="error" label="失败原因" min-width="180" show-overflow-tooltip />
        <el-table-column label="时间" min-width="175">
          <template #default="{ row }">{{ date(row.createdAt) }}</template>
        </el-table-column>
      </DataList>
    </LoadingContent>
  </div>
  <AppDialog
    v-model="open"
    :title="(editing ? '编辑' : '添加') + (tab === 'rules' ? '告警规则' : '通知渠道')"
    width="760px"
    destroy-on-close
    :close-on-click-modal="false"
  >
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-form label-width="112px">
      <el-form-item label="名称" required><el-input v-model="name" maxlength="80" /></el-form-item>
      <el-form-item label="类型">
        <el-select v-model="config.type">
          <el-option
            v-for="(label, key) in tab === 'rules' ? types : providers"
            :key="key"
            :value="key"
            :label="label"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="启用"><el-switch v-model="config.enabled" /></el-form-item>
      <template v-if="tab === 'rules'">
        <el-form-item
          v-if="['service', 'website', 'certificate'].includes(config.type)"
          label="监测对象"
        >
          <el-select v-model="config.targetId" filterable>
            <el-option v-for="r in targets" :key="r.id" :value="r.id" :label="r.name" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="['disk', 'inode'].includes(config.type)" label="挂载目录">
          <el-input v-model="config.path" placeholder="/" />
        </el-form-item>
        <el-form-item
          v-if="['cpu', 'memory', 'disk', 'inode', 'certificate'].includes(config.type)"
          :label="config.type === 'certificate' ? '剩余天数' : '使用率阈值 %'"
        >
          <el-input-number
            v-model="config.threshold"
            :min="0"
            :max="config.type === 'certificate' ? 365 : 100"
          />
        </el-form-item>
        <el-form-item label="连续次数">
          <el-input-number v-model="config.samples" :min="1" :max="60" />
          <span class="form-help">每分钟评估；事件类即时通知</span>
        </el-form-item>
        <el-form-item label="冷却分钟">
          <el-input-number v-model="config.cooldownMinutes" :min="1" :max="1440" />
        </el-form-item>
        <el-form-item label="通知渠道" required>
          <el-select v-model="config.channels" multiple>
            <el-option v-for="r in channels.data.value" :key="r.id" :value="r.id" :label="r.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="静默窗口">
          <el-time-select
            v-model="config.quietStart"
            start="00:00"
            end="23:30"
            step="00:30"
            placeholder="开始"
          />
          <el-time-select
            v-model="config.quietEnd"
            start="00:00"
            end="23:30"
            step="00:30"
            placeholder="结束"
          />
        </el-form-item>
        <el-form-item label="恢复通知"><el-switch v-model="config.recovery" /></el-form-item>
      </template>
      <template v-else>
        <template v-if="config.type === 'smtp'">
          <el-form-item label="SMTP 主机"><el-input v-model="config.host" /></el-form-item>
          <el-form-item label="端口">
            <el-input-number v-model="config.port" :min="1" :max="65535" />
          </el-form-item>
          <el-form-item label="用户名">
            <el-input v-model="config.username" autocomplete="off" />
          </el-form-item>
          <el-form-item label="密码">
            <el-input
              v-model="config.password"
              type="password"
              show-password
              autocomplete="new-password"
            />
          </el-form-item>
          <el-form-item label="发件人"><el-input v-model="config.from" /></el-form-item>
          <el-form-item label="收件人">
            <el-input v-model="config.to" placeholder="多个地址用逗号分隔" />
          </el-form-item>
          <p class="form-help">465 使用 TLS，其他端口要求 STARTTLS；证书必须有效。</p>
        </template>
        <template v-else>
          <el-form-item label="Webhook URL">
            <el-input v-model="config.url" type="password" show-password autocomplete="off" />
          </el-form-item>
          <el-form-item v-if="['dingtalk', 'feishu'].includes(config.type)" label="签名密钥">
            <el-input
              v-model="config.secret"
              type="password"
              show-password
              autocomplete="new-password"
            />
          </el-form-item>
          <el-form-item v-if="config.type === 'webhook'" label="Bearer Token">
            <el-input
              v-model="config.token"
              type="password"
              show-password
              autocomplete="new-password"
            />
          </el-form-item>
        </template>
        <el-form-item label="访问内网">
          <el-checkbox v-model="config.allowPrivate">
            允许私有网络目标，元数据地址始终禁止
          </el-checkbox>
        </el-form-item>
      </template>
    </el-form>
    <template #footer>
      <el-button @click="open = false">取消</el-button>
      <el-button type="primary" :loading="busy" @click="save">保存</el-button>
    </template>
  </AppDialog>
</template>
