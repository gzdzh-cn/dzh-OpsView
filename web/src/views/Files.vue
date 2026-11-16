<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Folder, Document, Upload, Plus, ArrowLeft, RefreshRight } from '@element-plus/icons-vue'
import { usePanel } from '../stores/panel'
import { post, errorText, size, date, toBase64, fromBase64 } from '../lib/api'
import CodeEditor from '../components/CodeEditor.vue'
const panel = usePanel(),
  selected = ref(''),
  path = ref(''),
  items = ref<any[]>([]),
  root = ref(''),
  error = ref(''),
  busy = ref(false),
  editOpen = ref(false),
  editPath = ref(''),
  content = ref(''),
  upload = ref<HTMLInputElement>()
const resources = computed(() =>
  panel.resources.filter((r) => ['site', 'app', 'instance'].includes(r.kind)),
)
async function op(action: string, p: string, extra: Record<string, any> = {}) {
  return post('/files', { resourceId: selected.value, action, path: p, ...extra })
}
async function load() {
  if (!selected.value) return
  busy.value = true
  error.value = ''
  try {
    const data = await op('list', path.value)
    items.value = data.items
    root.value = data.root
  } catch (e) {
    error.value = errorText(e)
    items.value = []
  } finally {
    busy.value = false
  }
}
watch(selected, () => {
  path.value = ''
  void load()
})
function join(name: string) {
  return path.value ? path.value + '/' + name : name
}
async function open(file: any) {
  if (file.symlink) {
    ElMessage.warning('不允许访问符号链接')
    return
  }
  if (file.directory) {
    path.value = join(file.name)
    await load()
    return
  }
  try {
    const data = await op('read', join(file.name))
    editPath.value = join(file.name)
    content.value = fromBase64(data.content)
    editOpen.value = true
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function save() {
  try {
    await op('write', editPath.value, { content: toBase64(content.value) })
    editOpen.value = false
    await load()
    ElMessage.success('文件已保存')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function mkdir() {
  try {
    const r = await ElMessageBox.prompt('输入目录名称', '新建目录', {
      confirmButtonText: '创建',
      cancelButtonText: '取消',
      inputPattern: /^[A-Za-z0-9_.-]+$/,
      inputErrorMessage: '目录名称格式无效',
    })
    await op('mkdir', join(r.value))
    await load()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function newFile() {
  try {
    const r = await ElMessageBox.prompt('输入文件名称', '新建文件', {
      confirmButtonText: '创建',
      cancelButtonText: '取消',
      inputPattern: /^[A-Za-z0-9_.-]+$/,
      inputErrorMessage: '文件名称格式无效',
    })
    editPath.value = join(r.value)
    content.value = ''
    editOpen.value = true
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function fileAction(file: any, action: string) {
  try {
    if (action === 'delete') {
      await ElMessageBox.confirm(`删除 ${file.name}？目录将连同内容一起移除。`, '删除文件', {
        type: 'warning',
        confirmButtonText: '删除',
        cancelButtonText: '取消',
      })
    }
    if (action === 'download') {
      const data = await op('read', join(file.name))
      const bytes = Uint8Array.from(atob(data.content), (c) => c.charCodeAt(0))
      const url = URL.createObjectURL(new Blob([bytes]))
      const a = document.createElement('a')
      a.href = url
      a.download = file.name
      a.click()
      URL.revokeObjectURL(url)
      return
    }
    let extra: Record<string, any> = {}
    if (action === 'chmod') {
      const r = await ElMessageBox.prompt('输入八进制权限，禁止 SUID/SGID', '修改权限', {
        inputValue: file.mode,
        confirmButtonText: '保存',
        cancelButtonText: '取消',
      })
      extra.mode = r.value
    }
    await op(action, join(file.name), extra)
    await load()
    ElMessage.success('操作完成')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function onUpload(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  if (file.size > 10 * 1024 * 1024) {
    ElMessage.error('首版单文件上传限制 10 MiB，大文件可通过服务器传输后刷新')
    return
  }
  busy.value = true
  try {
    const buffer = await file.arrayBuffer()
    let text = ''
    for (const b of new Uint8Array(buffer)) text += String.fromCharCode(b)
    await op('write', join(file.name), { content: btoa(text) })
    await load()
    ElMessage.success('文件上传完成')
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    busy.value = false
    ;(event.target as HTMLInputElement).value = ''
  }
}
function back() {
  path.value = path.value.split('/').slice(0, -1).join('/')
  void load()
}
</script>
<template>
  <div class="notice">
    文件访问限定在受管理资源目录。网站目录用于静态文件，应用目录用于部署产物，软件实例目录仅用于上传
    SQL 导入文件。
  </div>
  <section class="surface">
    <div class="page-toolbar">
      <div class="page-toolbar-left">
        <h3>资源文件</h3>
        <el-select v-model="selected" placeholder="选择网站、应用或数据库实例" style="width: 280px">
          <el-option
            v-for="r in resources"
            :key="r.id"
            :value="r.id"
            :label="`${r.name} · ${r.kind === 'instance' ? '导入目录' : r.kind === 'site' ? '站点文件' : '应用目录'}`"
          />
        </el-select>
      </div>
      <div v-if="selected" class="page-toolbar-right">
        <el-button :icon="RefreshRight" :loading="busy" @click="load">刷新</el-button>
        <el-dropdown @command="(v: string) => (v === 'folder' ? mkdir() : newFile())">
          <el-button :icon="Plus">新建</el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="file">文件</el-dropdown-item>
              <el-dropdown-item command="folder">目录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-button type="primary" :icon="Upload" @click="upload?.click()">上传文件</el-button>
        <input ref="upload" type="file" hidden @change="onUpload" />
      </div>
    </div>
    <div v-if="selected" class="file-path">
      <button v-if="path" @click="back">
        <el-icon><ArrowLeft /></el-icon>
      </button>
      <el-icon><Folder /></el-icon>
      <span>{{ root || '受管理目录' }}/{{ path }}</span>
    </div>
    <div v-if="error" class="notice warn" style="margin: 15px">{{ error }}</div>
    <el-table v-if="items.length" :data="items">
      <el-table-column label="文件名" min-width="220">
        <template #default="{ row }">
          <a class="resource-identity" href="#" @click.prevent="open(row)">
            <span class="resource-avatar">
              <Folder v-if="row.directory" />
              <Document v-else />
            </span>
            <span>{{ row.name }}{{ row.symlink ? ' ↗' : '' }}</span>
          </a>
        </template>
      </el-table-column>
      <el-table-column label="大小" width="100">
        <template #default="{ row }">{{ row.directory ? '—' : size(row.size) }}</template>
      </el-table-column>
      <el-table-column label="权限" width="90" prop="mode" />
      <el-table-column label="修改时间" min-width="160">
        <template #default="{ row }">
          <span class="cell-date">{{ date(row.modifiedAt) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" min-width="200">
        <template #default="{ row }">
          <div class="row-actions">
            <button v-if="!row.directory" @click="open(row)">编辑</button>
            <button v-if="!row.directory" @click="fileAction(row, 'download')">下载</button>
            <button v-if="row.name.endsWith('.zip')" @click="fileAction(row, 'extract')">
              解压
            </button>
            <button @click="fileAction(row, 'chmod')">权限</button>
            <button class="danger" @click="fileAction(row, 'delete')">删除</button>
          </div>
        </template>
      </el-table-column>
    </el-table>
    <div v-else class="empty-page">
      <span class="empty-symbol"><Folder /></span>
      <h3>{{ selected ? '目录为空' : '选择一个资源目录' }}</h3>
      <p>
        {{
          selected
            ? '上传构建产物、静态文件或 ZIP 压缩包，开始部署。'
            : '先创建网站、应用或软件实例，然后管理它的文件。'
        }}
      </p>
    </div>
  </section>
  <el-dialog
    v-model="editOpen"
    :title="editPath"
    width="900px"
    :close-on-click-modal="false"
    destroy-on-close
  >
    <CodeEditor v-model="content" />
    <template #footer>
      <el-button @click="editOpen = false">取消</el-button>
      <el-button type="primary" @click="save">保存文件</el-button>
    </template>
  </el-dialog>
</template>
