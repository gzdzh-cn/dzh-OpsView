<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, errorText, type Config, type Resource } from '../lib/api'
import { usePageQuery } from '../composables/pageQuery'
import DialogActions from './DialogActions.vue'
const props = defineProps<{ site: Resource }>()
const rules = usePageQuery<Resource[]>('/notifications/rules', [])
const channels = usePageQuery<Resource[]>('/notifications/channels', [])
const rows = computed(() =>
  rules.data.value.filter(
    (v) =>
      v.config.targetId === props.site.id && ['website', 'certificate'].includes(v.config.type),
  ),
)
const form = ref<Config>(),
  editing = ref<Resource>(),
  busy = ref(false),
  error = ref('')
function edit(row?: Resource) {
  editing.value = row
  form.value = row
    ? JSON.parse(JSON.stringify(row.config))
    : {
        type: 'website',
        targetId: props.site.id,
        threshold: 7,
        samples: 3,
        cooldownMinutes: 15,
        recovery: true,
        enabled: true,
        channels: [],
      }
}
async function save() {
  if (!form.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await api('/notifications/rules' + (editing.value ? '/' + editing.value.id : ''), {
      method: editing.value ? 'PUT' : 'POST',
      body: JSON.stringify({
        name: props.site.name + (form.value.type === 'certificate' ? ' · 证书到期' : ' · 可用性'),
        config: form.value,
        revision: editing.value?.revision,
      }),
    })
    form.value = undefined
    await rules.load(true)
    ElMessage.success('告警已保存')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function remove(row: Resource) {
  try {
    await ElMessageBox.confirm('删除该网站告警规则？', '删除告警', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
    await api('/notifications/rules/' + row.id, { method: 'DELETE' })
    await rules.load(true)
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  }
}
</script>
<template>
  <el-alert
    v-if="error || rules.error.value || channels.error.value"
    :title="error || rules.error.value || channels.error.value"
    type="error"
    :closable="false"
  />
  <template v-if="form">
    <el-form label-width="104px">
      <el-form-item label="告警类型">
        <el-select v-model="form.type">
          <el-option label="网站不可用" value="website" />
          <el-option label="证书到期" value="certificate" />
        </el-select>
      </el-form-item>
      <el-form-item label="开启告警"><el-switch v-model="form.enabled" /></el-form-item>
      <el-form-item v-if="form.type === 'certificate'" label="剩余天数">
        <el-input-number v-model="form.threshold" :min="0" :max="365" />
      </el-form-item>
      <el-form-item label="连续次数">
        <el-input-number v-model="form.samples" :min="1" :max="60" />
      </el-form-item>
      <el-form-item label="冷却时间">
        <el-input-number v-model="form.cooldownMinutes" :min="1" :max="1440" />
        <small>分钟</small>
      </el-form-item>
      <el-form-item label="恢复通知"><el-switch v-model="form.recovery" /></el-form-item>
      <el-form-item label="通知渠道">
        <el-select v-model="form.channels" multiple placeholder="选择已配置渠道">
          <el-option
            v-for="item in channels.data.value"
            :key="item.id"
            :value="item.id"
            :label="item.name + (item.config.enabled ? '' : '（已暂停）')"
          />
        </el-select>
      </el-form-item>
    </el-form>
    <DialogActions>
      <el-button :disabled="busy" @click="form = undefined">返回列表</el-button>
      <el-button type="primary" :loading="busy" @click="save">保存告警</el-button>
    </DialogActions>
  </template>
  <template v-else>
    <div class="page-toolbar">
      <el-button @click="edit()">添加网站告警</el-button>
      <el-button :loading="rules.busy.value" @click="rules.load(true)">刷新</el-button>
    </div>
    <el-table :data="rows">
      <el-table-column label="告警" prop="name" min-width="150" />
      <el-table-column label="状态" width="80">
        <template #default="{ row }">{{ row.config.enabled ? '开启' : '暂停' }}</template>
      </el-table-column>
      <el-table-column label="执行频率" width="100">
        <template #default>每分钟</template>
      </el-table-column>
      <el-table-column label="操作" width="120">
        <template #default="{ row }">
          <el-button link @click="edit(row)">修改</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <p class="form-help">
      复用面板告警服务，每分钟评估；网站探测使用真实访问结果。通知渠道在面板设置中统一管理。
    </p>
  </template>
</template>
