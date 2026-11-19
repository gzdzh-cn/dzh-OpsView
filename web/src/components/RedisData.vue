<script setup lang="ts">
import DialogActions from './DialogActions.vue'
import AppDialog from './AppDialog.vue'
import { ref, watch, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, downloadPOST, toBase64, errorText, type Config, type Resource } from '../lib/api'
import { usePageState } from '../composables/pageState'
const props = defineProps<{ instance: Resource }>()
const db = usePageState(() => `redis:${props.instance.id}:db`, 0),
  pattern = usePageState(() => `redis:${props.instance.id}:pattern`, ''),
  type = usePageState(() => `redis:${props.instance.id}:type`, '')
const count = ref(16),
  rows = ref<Config[]>([]),
  cursor = ref('0'),
  started = ref(false),
  busy = ref(false),
  error = ref(''),
  open = ref(false),
  current = ref<Config>({}),
  content = ref<Config>({}),
  draft = ref<Config>({}),
  binary = ref(false)
let sequence = 0,
  readSequence = 0,
  controller: AbortController | undefined
const kinds = ['string', 'hash', 'list', 'set', 'zset', 'stream']
async function request(operation: string, p: Config, signal?: AbortSignal) {
  return api(`/redis/${props.instance.id}/${operation}`, {
    method: 'POST',
    body: JSON.stringify({ db: db.value, ...p }),
    signal,
  })
}
async function scan(reset = true) {
  const seq = ++sequence
  controller?.abort()
  controller = new AbortController()
  busy.value = true
  error.value = ''
  if (reset) {
    rows.value = []
    cursor.value = '0'
    started.value = false
  }
  try {
    const result = await request(
      'scan',
      { cursor: cursor.value, pattern: pattern.value, type: type.value },
      controller.signal,
    )
    if (seq !== sequence) return
    const map = new Map(rows.value.map((r) => [r.key, r]))
    for (const r of result.rows) map.set(r.key, r)
    rows.value = [...map.values()]
    cursor.value = result.cursor
    started.value = true
  } catch (e) {
    if (seq === sequence && !(e instanceof DOMException && e.name === 'AbortError'))
      error.value = errorText(e)
  } finally {
    if (seq === sequence) busy.value = false
  }
}
watch(
  () => props.instance.id,
  async () => {
    try {
      const result = await request('databases', {})
      count.value = Math.min(65536, result.count)
    } catch (e) {
      error.value = errorText(e)
    }
    await scan()
  },
  { immediate: true },
)
watch(db, () => {
  open.value = false
  void scan()
})
watch(open, () => {
  if (!open.value) {
    readSequence++
    content.value = {}
    draft.value = {}
  }
})
onBeforeUnmount(() => {
  sequence++
  readSequence++
  controller?.abort()
  content.value = {}
  draft.value = {}
})
async function inspect(row: Config, next = '0') {
  current.value = row
  open.value = true
  const seq = ++readSequence
  error.value = ''
  content.value = {}
  draft.value = {}
  try {
    const result = await request('read', { key: row.key, cursor: next })
    if (seq !== readSequence || !open.value) return
    content.value = result
    draft.value = {
      operation: 'set',
      type: result.type,
      key: row.key,
      revision: result.revision,
      value:
        result.type === 'string'
          ? result.content?.binary
            ? result.content.base64
            : result.content?.text || ''
          : '',
      field: '',
      score: '0',
      index: '',
      ttl: result.ttlMillis < 0 ? -1 : Math.ceil(result.ttlMillis / 1000),
    }
    binary.value = !!result.content?.binary
  } catch (e) {
    if (seq === readSequence) error.value = errorText(e)
  }
}
function create() {
  readSequence++
  current.value = {}
  content.value = { editable: true, revision: 'missing' }
  draft.value = {
    operation: 'set',
    type: 'string',
    revision: 'missing',
    name: '',
    value: '',
    field: '',
    score: '0',
    index: '',
  }
  binary.value = false
  open.value = true
}
async function save(operation = 'set') {
  busy.value = true
  error.value = ''
  try {
    const p: Config = {
      ...draft.value,
      operation,
      key: current.value.key || toBase64(draft.value.name || ''),
      revision: content.value.revision,
      value: binary.value ? draft.value.value : toBase64(draft.value.value || ''),
      field: binary.value ? draft.value.field : toBase64(draft.value.field || ''),
    }
    if (operation === 'remove-member' && draft.value.type === 'hash') p.value = p.field
    if (operation === 'rename') p.target = toBase64(draft.value.target || '')
    if (operation === 'delete') {
      await ElMessageBox.confirm('删除此键不会进入数据库回收站，确认删除？', '删除 Redis 键', {
        type: 'warning',
      })
      p.confirmed = true
    }
    await request('write', p)
    ElMessage.success('已保存')
    open.value = false
    await scan()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function download() {
  try {
    await downloadPOST(
      `/redis/${props.instance.id}/download`,
      { db: db.value, key: current.value.key },
      'redis-value.redis-dump',
    )
  } catch (e) {
    error.value = errorText(e)
  }
}
</script>
<template>
  <div class="page-toolbar">
    <div class="page-toolbar-left">
      <el-select v-model="db" aria-label="Redis 逻辑库" style="width: 120px">
        <el-option v-for="n in count" :key="n" :value="n - 1" :label="`DB ${n - 1}`" />
      </el-select>
      <el-input
        v-model="pattern"
        placeholder="键名匹配，例如 session:*"
        aria-label="Redis 键搜索"
        clearable
        style="width: 260px"
        @keyup.enter="scan()"
      />
      <el-select v-model="type" clearable placeholder="全部类型" style="width: 140px">
        <el-option v-for="k in kinds" :key="k" :value="k" :label="k" />
      </el-select>
      <el-button :loading="busy" @click="scan()">查询</el-button>
    </div>
    <el-button type="primary" @click="create">添加键</el-button>
  </div>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <el-table :data="rows" v-loading="busy">
    <el-table-column prop="name" label="键名" min-width="220" show-overflow-tooltip>
      <template #default="{ row }">{{ row.binary ? 'Base64: ' + row.key : row.name }}</template>
    </el-table-column>
    <el-table-column prop="type" label="类型" width="110" />
    <el-table-column label="大小/元素数量" width="160">
      <template #default="{ row }">
        {{ row.length }} {{ row.type === 'string' ? '字节' : '项' }}
      </template>
    </el-table-column>
    <el-table-column label="TTL" width="140">
      <template #default="{ row }">
        {{
          row.ttlMillis === -1
            ? '永久'
            : row.ttlMillis < 0
              ? '已过期'
              : Math.ceil(row.ttlMillis / 1000) + ' 秒'
        }}
      </template>
    </el-table-column>
    <el-table-column label="操作" width="100">
      <template #default="{ row }"><el-button link @click="inspect(row)">管理</el-button></template>
    </el-table-column>
  </el-table>
  <div class="list-pagination">
    <small>已加载 {{ rows.length }} 个键 · 扫描期间数据可能变化</small>
    <el-button v-if="started && cursor !== '0'" :loading="busy" @click="scan(false)">
      加载更多
    </el-button>
    <small v-else-if="started">本轮扫描完成</small>
  </div>
  <AppDialog
    v-model="open"
    :title="current.key ? 'Redis 键管理' : '添加 Redis 键'"
    width="min(850px,94vw)"
  >
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <template v-if="current.key">
      <div class="page-toolbar">
        <code>{{ current.binary ? current.key : current.name }}</code>
        <el-tag>{{ content.type }}</el-tag>
        <el-button @click="download">下载值</el-button>
      </div>
      <el-alert v-if="content.notice" :title="content.notice" type="info" :closable="false" />
      <pre class="redis-content">{{ JSON.stringify(content.content, null, 2) }}</pre>
      <el-button
        v-if="content.cursor && content.cursor !== '0'"
        @click="inspect(current, content.cursor)"
      >
        下一段
      </el-button>
    </template>
    <el-form v-if="content.editable" label-width="100px">
      <el-form-item v-if="!current.key" label="键名">
        <el-input v-model="draft.name" />
      </el-form-item>
      <el-form-item label="类型">
        <el-select v-model="draft.type" :disabled="!!current.key">
          <el-option
            v-for="k in kinds.filter((k) => k !== 'stream')"
            :key="k"
            :value="k"
            :label="k"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="编码">
        <el-switch v-model="binary" active-text="Base64" inactive-text="UTF-8" />
      </el-form-item>
      <el-form-item v-if="draft.type === 'hash'" label="字段">
        <el-input v-model="draft.field" />
      </el-form-item>
      <el-form-item v-if="draft.type === 'zset'" label="分数">
        <el-input v-model="draft.score" />
      </el-form-item>
      <el-form-item v-if="draft.type === 'list'" label="下标">
        <el-input v-model="draft.index" placeholder="留空追加，填写下标替换元素" />
      </el-form-item>
      <el-form-item label="值/成员">
        <el-input
          v-model="draft.value"
          type="textarea"
          :rows="5"
          placeholder="单次编辑最多 1 MiB"
        />
      </el-form-item>
      <DialogActions>
        <el-button type="primary" :loading="busy" @click="save()">保存（保留 TTL）</el-button>
        <el-button
          v-if="current.key && ['hash', 'list', 'set', 'zset'].includes(draft.type)"
          @click="save('remove-member')"
        >
          删除指定成员
        </el-button>
      </DialogActions>
      <template v-if="current.key">
        <el-form-item label="TTL 秒">
          <el-input-number v-model="draft.ttl" :min="-1" :max="2147483647" />
          <el-button @click="save('ttl')">应用 TTL</el-button>
          <small>−1 永久，0 立即过期</small>
        </el-form-item>
        <el-form-item label="重命名">
          <el-input v-model="draft.target" />
          <el-button @click="save('rename')">重命名</el-button>
        </el-form-item>
        <DialogActions>
          <el-button type="danger" @click="save('delete')">删除键</el-button>
        </DialogActions>
      </template>
    </el-form>
    <p v-else>此类型或大小当前仅支持只读查看。</p>
  </AppDialog>
</template>
<style scoped>
.redis-content {
  max-height: 300px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: var(--el-fill-color-light);
  padding: 12px;
}
code {
  overflow-wrap: anywhere;
}
.el-form-item .el-input {
  max-width: 550px;
}
</style>
