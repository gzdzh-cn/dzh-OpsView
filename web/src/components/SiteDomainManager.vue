<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { errorText, type Config } from '../lib/api'
const config = defineModel<Config>({ required: true })
const props = defineProps<{
  committed: Config
  siteId: string
  port: number
  busy: boolean
  child?: boolean
  directories: string[]
  apply: () => Promise<boolean>
}>()
const input = ref(''),
  directory = ref(''),
  error = ref(''),
  checked = ref<string[]>([])
watch(
  () => [props.siteId, props.child],
  () => {
    input.value = ''
    error.value = ''
    checked.value = []
  },
)
function parse(value: string) {
  if (value.startsWith('[')) {
    const match = value.match(/^\[([^\]]+)\]:(\d+)$/)
    if (match) return { host: match[1]!, port: Number(match[2]) }
  }
  const match = value.match(/^([^:]+):(\d+)$/)
  return match ? { host: match[1]!, port: Number(match[2]) } : { host: value, port: props.port }
}
const rows = computed(() =>
  props.child
    ? (props.committed.subdirectoryBindings || []).map((b: Config, index: number) => ({
        ...b,
        key: String(index),
        port: b.port || props.port,
      }))
    : String(props.committed.domains || '')
        .split(/\s+/)
        .filter(Boolean)
        .map((value, index) => ({ ...parse(value), value, key: value, primary: index === 0 })),
)
function link(row: Config) {
  const host = String(row.host).replace(/^\*\./, '')
  const secure = !!props.committed.tls && row.port === props.port
  return `${secure ? 'https' : 'http'}://${host.includes(':') ? '[' + host + ']' : host}${secure || row.port === 80 ? '' : ':' + row.port}/`
}
async function add() {
  error.value = ''
  const values = input.value.toLowerCase().split(/\s+/).filter(Boolean)
  if (!values.length) {
    error.value = '请填写需要绑定的域名，每行一个'
    return
  }
  if (props.child && !directory.value) {
    error.value = '请选择子目录'
    return
  }
  if (props.child)
    config.value.subdirectoryBindings = [
      ...(props.committed.subdirectoryBindings || []),
      ...values.map((v) => ({ ...parse(v), directory: directory.value })),
    ]
  else
    config.value.domains = [
      ...String(props.committed.domains || '')
        .split(/\s+/)
        .filter(Boolean),
      ...values,
    ].join('\n')
  if (await props.apply()) {
    input.value = ''
    checked.value = []
  }
}
async function remove(keys: string[]) {
  try {
    await ElMessageBox.confirm(`将解除 ${keys.length} 个域名绑定，网站文件保留。`, '解除域名绑定', {
      confirmButtonText: '解除绑定',
      cancelButtonText: '取消',
    })
    if (props.child)
      config.value.subdirectoryBindings = (config.value.subdirectoryBindings || []).filter(
        (_: Config, i: number) => !keys.includes(String(i)),
      )
    else
      config.value.domains = String(config.value.domains || '')
        .split(/\s+/)
        .filter((v) => v && !keys.includes(v))
        .join('\n')
    if (await props.apply()) checked.value = []
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  }
}
</script>
<template>
  <div class="site-domain-manager">
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-input
      v-model="input"
      aria-label="添加绑定域名"
      type="textarea"
      :rows="3"
      placeholder="每行一个域名，例如 www.example.com&#10;自定义端口：www.example.com:8080&#10;泛域名：*.example.com；IPv6：[地址]:8080"
      :disabled="busy"
    />
    <div class="page-toolbar">
      <el-select
        v-if="child"
        v-model="directory"
        filterable
        allow-create
        default-first-option
        placeholder="选择子目录"
        aria-label="绑定子目录"
      >
        <el-option v-for="dir in directories" :key="dir" :value="dir" :label="dir" />
      </el-select>
      <el-button type="primary" :loading="busy" @click="add">添加域名</el-button>
      <el-button :disabled="busy || !checked.length" @click="remove(checked)">批量解除</el-button>
      <small class="form-help">
        默认端口 {{ port }}；HTTPS 使用证书设置。{{
          child ? '目录相对于项目根目录。' : '首个域名为主域名。'
        }}
      </small>
    </div>
    <el-table
      :data="rows"
      row-key="key"
      @selection-change="(items: Config[]) => (checked = items.map((r) => r.key))"
    >
      <el-table-column type="selection" width="42" :selectable="(row: Config) => !row.primary" />
      <el-table-column label="域名" min-width="170" show-overflow-tooltip>
        <template #default="{ row }">
          <a
            :href="link(row)"
            target="_blank"
            rel="noopener noreferrer"
            :title="
              row.host.startsWith('*.')
                ? '打开根域名；泛域名需要使用实际子域名访问'
                : '在新标签页打开'
            "
          >
            {{ row.host }}
          </a>
          <el-tag v-if="row.primary" size="small" type="info">主域名</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="端口" prop="port" width="78" />
      <el-table-column
        v-if="child"
        label="目录"
        prop="directory"
        min-width="100"
        show-overflow-tooltip
      />
      <el-table-column label="操作" width="94">
        <template #default="{ row }">
          <span v-if="row.primary" class="form-help">主域名</span>
          <el-button v-else link type="danger" :disabled="busy" @click="remove([row.key])">
            解除
          </el-button>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>
<style scoped>
.site-domain-manager {
  display: grid;
  gap: 12px;
  min-width: 0;
}
.site-domain-manager a {
  color: var(--el-color-primary);
  text-decoration: none;
}
.site-domain-manager .el-tag {
  margin-left: 6px;
}
</style>
