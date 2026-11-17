<script setup lang="ts">
import { usePageState } from '../composables/pageState'
import { isAbort } from '../lib/query'
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Folder, Document, ArrowLeft, RefreshRight } from '@element-plus/icons-vue'
import { api, post, errorText, size, date, type Config } from '../lib/api'
import ListPanel from '../components/ListPanel.vue'
import DataList from '../components/DataList.vue'
import CodeEditor from '../components/CodeEditor.vue'
const route = useRoute(),
  path = usePageState('path', '/'),
  address = ref('/'),
  items = ref<Config[]>([]),
  selection = ref<Config[]>([]),
  metadata = ref<Config>({}),
  total = ref(0),
  page = usePageState('page', 1),
  pageSize = usePageState('pageSize', 50),
  filter = usePageState('filter', ''),
  hidden = usePageState('hidden', false),
  sort = usePageState('sort', 'name'),
  descending = usePageState('descending', false),
  busy = ref(false),
  error = ref(''),
  readOnly = ref(false)
function savedRecent(): string[] {
  try {
    const value = JSON.parse(localStorage.getItem('opsview:file-recent') || '[]')
    return Array.isArray(value)
      ? value.filter((p): p is string => typeof p === 'string').slice(0, 15)
      : []
  } catch {
    return []
  }
}
const recent = ref<string[]>([])
const breadcrumb = computed(() =>
  path.value
    .split('/')
    .filter(Boolean)
    .map((name, i, all) => ({ name, path: '/' + all.slice(0, i + 1).join('/') })),
)
const editOpen = ref(false),
  editPath = ref(''),
  original = ref(''),
  content = ref(''),
  revision = ref(''),
  saving = ref(false),
  diffOpen = ref(false)
const changes = computed(() => {
  const a = original.value.split('\n'),
    b = content.value.split('\n')
  const lines: string[] = []
  for (let i = 0; i < Math.max(a.length, b.length) && lines.length < 200; i++) {
    if (a[i] !== b[i]) {
      if (a[i] !== undefined) lines.push(`- ${i + 1}: ${a[i]}`)
      if (b[i] !== undefined) lines.push(`+ ${i + 1}: ${b[i]}`)
    }
  }
  return lines.join('\n') || '内容没有变化'
})
const clipboard = ref<{ paths: string[]; action: 'copy' | 'move' }>(),
  taskId = ref(''),
  taskBusy = ref(false),
  taskResult = ref<Config>(),
  resultOpen = ref(false),
  resultTitle = ref('文件任务结果'),
  resultJSON = computed(() => (taskResult.value ? JSON.stringify(taskResult.value, null, 2) : ''))
const trashOpen = ref(false),
  trash = ref<Config[]>([]),
  uploadInput = ref<HTMLInputElement>(),
  uploadBusy = ref(false),
  uploadName = ref(''),
  uploadProgress = ref(0),
  uploadSession = ref(''),
  uploadPaused = ref(false)
let controller: AbortController | undefined,
  alive = true
const join = (base: string, name: string) => (base === '/' ? '' : base) + '/' + name
const parent = (p: string) => p.slice(0, p.lastIndexOf('/')) || '/'
const op = (action: string, params: Config = {}, signal?: AbortSignal) =>
  api('/files', {
    method: 'POST',
    body: JSON.stringify({ scope: 'server', action, ...params }),
    signal,
  })
let listing: AbortController | undefined,
  listSequence = 0
const metadataController = new AbortController()
function remember(p: string) {
  recent.value = [p, ...recent.value.filter((x) => x !== p)].slice(0, 15)
  localStorage.setItem('opsview:file-recent', JSON.stringify(recent.value))
}
async function load() {
  const id = ++listSequence
  listing?.abort()
  listing = new AbortController()
  busy.value = true
  error.value = ''
  try {
    const data = await op(
      'list',
      {
        path: path.value,
        page: page.value,
        pageSize: pageSize.value,
        filter: filter.value,
        hidden: hidden.value,
        sort: sort.value,
        descending: descending.value,
      },
      listing.signal,
    )
    if (!alive || id !== listSequence) return
    items.value = data.items
    total.value = data.total
    path.value = data.path
    address.value = data.path
    readOnly.value = data.readOnly
    selection.value = []
    remember(path.value)
  } catch (e) {
    if (alive && id === listSequence && !isAbort(e)) {
      error.value = errorText(e)
      items.value = []
    }
  } finally {
    if (alive && id === listSequence) busy.value = false
  }
}
async function navigate(p: string) {
  path.value = p || '/'
  page.value = 1
  await load()
}
async function refreshMeta() {
  try {
    metadata.value = await op('metadata', { recentPaths: savedRecent() }, metadataController.signal)
    recent.value = [
      ...new Set<string>([...recent.value, ...(metadata.value.recentPaths || [])]),
    ].slice(0, 15)
    localStorage.setItem('opsview:file-recent', JSON.stringify(recent.value))
  } catch (e) {
    error.value = errorText(e)
  }
}
async function open(row: Config) {
  if (row.protected || row.special) {
    ElMessage.warning('该项仅展示基础信息或受到保护')
    return
  }
  try {
    if (row.directory || (row.symlink && row.targetDirectory)) {
      await navigate(row.path)
      return
    }
    await edit(row.path)
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function edit(p: string) {
  try {
    const data = await op('read', { path: p })
    error.value = ''
    editPath.value = p
    content.value = data.text
    original.value = data.text
    revision.value = data.revision
    editOpen.value = true
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function save() {
  saving.value = true
  try {
    const data = await op('write', {
      path: editPath.value,
      text: content.value,
      revision: revision.value,
    })
    revision.value = data.revision
    original.value = content.value
    diffOpen.value = false
    editOpen.value = false
    await load()
    ElMessage.success('文件已保存')
  } catch (e) {
    error.value = errorText(e)
    ElMessage.error(errorText(e))
  } finally {
    saving.value = false
  }
}
async function create(directory: boolean) {
  try {
    const answer = await ElMessageBox.prompt(
      '输入名称，可使用中文及空格；不能包含 /、换行或空字节',
      directory ? '新建目录' : '新建文件',
      {
        inputValidator: (v) =>
          (!!v && v !== '.' && v !== '..' && !/[\/\x00\r\n]/.test(v)) || '名称无效',
        confirmButtonText: '创建',
        cancelButtonText: '取消',
      },
    )
    const full = join(path.value, answer.value)
    if (directory) {
      await op('mkdir', { path: full })
      await load()
    } else {
      editPath.value = full
      revision.value = ''
      original.value = ''
      content.value = ''
      editOpen.value = true
    }
  } catch (e) {
    showError(e)
  }
}
function showError(e: unknown) {
  if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
}
async function favorite(p: string, remove = false) {
  try {
    await op('favorite', { path: p, remove })
    await refreshMeta()
  } catch (e) {
    showError(e)
  }
}
async function batch(action: string, rows = selection.value, extra: Config = {}) {
  const selected = rows.filter((r) => !r.protected && !r.special)
  if (!selected.length) {
    ElMessage.warning('请选择可操作的文件')
    return
  }
  try {
    if (action === 'delete')
      await ElMessageBox.confirm(
        `将 ${selected.length} 项移入回收站，默认保留 30 天。`,
        '删除文件',
        { confirmButtonText: '移入回收站', cancelButtonText: '取消', type: 'warning' },
      )
    const data = await op(action, { paths: selected.map((r) => r.path), ...extra })
    if (data.id) await awaitTask(data.id, action === 'size' ? '目录大小' : '文件操作')
    else {
      ElMessage.success('操作完成')
      await load()
    }
  } catch (e) {
    showError(e)
  }
}
async function rename(row: Config) {
  try {
    const answer = await ElMessageBox.prompt('输入新名称', '重命名', {
      inputValue: row.name,
      inputValidator: (v) =>
        (!!v && !/[\/\x00\r\n]/.test(v) && v !== '.' && v !== '..') || '名称无效',
      confirmButtonText: '保存',
      cancelButtonText: '取消',
    })
    await batch('rename', [row], { target: join(parent(row.path), answer.value) })
  } catch (e) {
    showError(e)
  }
}
async function conflict() {
  const answer = await ElMessageBox.prompt(
    '同名处理：skip 跳过（默认）、keep 保留两份、overwrite 覆盖并保留回收副本',
    '冲突处理',
    {
      inputValue: 'skip',
      inputValidator: (v) =>
        ['skip', 'keep', 'overwrite'].includes(v) || '请输入 skip、keep 或 overwrite',
      confirmButtonText: '继续',
      cancelButtonText: '取消',
    },
  )
  return answer.value
}
async function paste() {
  if (!clipboard.value) return
  try {
    const policy = await conflict()
    const data = await op(clipboard.value.action, {
      paths: clipboard.value.paths,
      target: path.value,
      conflict: policy,
    })
    await awaitTask(data.id, '粘贴结果')
    clipboard.value = undefined
  } catch (e) {
    showError(e)
  }
}
async function note(row: Config) {
  try {
    const answer = await ElMessageBox.prompt('输入备注', '文件备注', {
      inputValue: row.note || '',
      confirmButtonText: '保存',
      cancelButtonText: '取消',
    })
    await op('note', { path: row.path, note: answer.value })
    await load()
  } catch (e) {
    showError(e)
  }
}
function download(row: Config) {
  const a = document.createElement('a')
  a.href = '/api/v1/files/transfer?' + new URLSearchParams({ path: row.path })
  a.download = row.name
  a.click()
}
async function awaitTask(id: string, title: string) {
  taskId.value = id
  taskBusy.value = true
  taskResult.value = undefined
  resultTitle.value = title
  try {
    while (alive) {
      const data = await api('/files/tasks/' + id)
      if (data.state === 'succeeded') {
        taskResult.value = data.result
        resultOpen.value = true
        await load()
        return
      }
      if (['failed', 'cancelled', 'interrupted'].includes(data.state))
        throw new Error(data.error || '任务已取消或中断')
      await new Promise((resolve) => setTimeout(resolve, 800))
    }
  } finally {
    taskBusy.value = false
    taskId.value = ''
  }
}
async function cancelTask() {
  try {
    await post(`/tasks/${taskId.value}/cancel`)
    ElMessage.info('已请求取消任务')
  } catch (e) {
    showError(e)
  }
}
async function search(contentSearch: boolean) {
  try {
    if (!filter.value.trim()) {
      ElMessage.warning('先输入搜索内容')
      return
    }
    const data = await op('search', {
      path: path.value,
      query: filter.value,
      content: contentSearch,
    })
    await awaitTask(data.id, contentSearch ? '文件内容搜索结果' : '递归名称搜索结果')
  } catch (e) {
    showError(e)
  }
}
async function calculate(row?: Config) {
  try {
    const data = await op('size', { path: row?.path || path.value })
    await awaitTask(data.id, '目录大小')
  } catch (e) {
    showError(e)
  }
}
async function archive(action: 'compress' | 'extract', row?: Config) {
  try {
    if (action === 'compress' && !selection.value.length) {
      ElMessage.warning('先选择文件或目录')
      return
    }
    const initial = action === 'compress' ? join(path.value, 'archive.zip') : path.value
    const answer = await ElMessageBox.prompt(
      action === 'compress'
        ? '输入压缩包完整路径，支持 .zip 或 .tar.gz'
        : '输入已存在的解压目标目录',
      '压缩 / 解压',
      { inputValue: initial, confirmButtonText: '继续', cancelButtonText: '取消' },
    )
    const policy = await conflict()
    const data = await op(action, {
      paths: selection.value.map((r) => r.path),
      path: row?.path,
      target: answer.value,
      conflict: policy,
    })
    await awaitTask(data.id, action === 'compress' ? '压缩结果' : '解压结果')
  } catch (e) {
    showError(e)
  }
}
const permissionOpen = ref(false),
  permissionRows = ref<Config[]>([]),
  permission = ref({ mode: '0644', owner: '', group: '', recursive: false }),
  permissionCount = ref('')
async function permissions(rows = selection.value) {
  if (!rows.length) return
  permissionRows.value = rows
  permission.value = {
    mode: rows[0].mode || '0644',
    owner: rows[0].owner || '',
    group: rows[0].group || '',
    recursive: false,
  }
  permissionCount.value = ''
  permissionOpen.value = true
}
async function permissionPreview() {
  try {
    let count = 0
    for (const row of permissionRows.value) {
      const task = await op('size', { path: row.path })
      await awaitTask(task.id, '权限影响范围')
      count += taskResult.value?.count || 0
    }
    permissionCount.value = `将影响 ${count} 项（不递归链接和其他挂载点）`
    resultOpen.value = false
  } catch (e) {
    showError(e)
  }
}
async function savePermissions() {
  try {
    if (permission.value.recursive && !permissionCount.value) {
      ElMessage.warning('请先计算影响范围')
      return
    }
    await ElMessageBox.confirm(
      permission.value.recursive
        ? permissionCount.value
        : `修改 ${permissionRows.value.length} 项的权限及所有者`,
      '确认权限变更',
      { confirmButtonText: '应用', cancelButtonText: '取消' },
    )
    await batch('permissions', permissionRows.value, { ...permission.value, confirmed: true })
    permissionOpen.value = false
  } catch (e) {
    showError(e)
  }
}
async function loadTrash() {
  try {
    trash.value = (await op('trash', { trashAction: 'list' })).items
    trashOpen.value = true
  } catch (e) {
    showError(e)
  }
}
async function trashAction(action: string, row?: Config) {
  try {
    let target = ''
    if (action === 'restore') {
      const answer = await ElMessageBox.prompt(
        '恢复到完整路径；已有同名项时请更换位置',
        '恢复文件',
        { inputValue: row?.original, confirmButtonText: '恢复', cancelButtonText: '取消' },
      )
      target = answer.value
    } else
      await ElMessageBox.confirm(
        action === 'empty' ? '永久清空回收站全部内容，无法恢复。' : '永久删除此项，无法恢复。',
        '永久删除',
        { type: 'warning', confirmButtonText: '永久删除', cancelButtonText: '取消' },
      )
    await op('trash', { trashAction: action, id: row?.id, target, confirmed: true })
    await loadTrash()
    await load()
  } catch (e) {
    showError(e)
  }
}
async function uploadFiles(event: Event) {
  const files = Array.from((event.target as HTMLInputElement).files || [])
  uploadBusy.value = true
  uploadPaused.value = false
  try {
    for (const file of files) {
      if (file.size > 2 * 1024 ** 3) throw new Error('单文件上传上限为 2 GiB')
      uploadName.value = file.name
      const target = join(path.value, file.name),
        key = 'opsview:upload:' + target + ':' + file.size + ':' + file.lastModified
      let id = localStorage.getItem(key) || '',
        offset = 0
      if (id) {
        try {
          offset = (await op('upload-status', { id })).offset
        } catch {
          id = ''
        }
      }
      if (!id) {
        let policy = 'skip'
        if (items.value.some((x) => x.name === file.name)) {
          const chosen = await conflict()
          if (chosen === 'skip') continue
          policy = chosen
        }
        const session = await op('upload-create', {
          path: target,
          size: file.size,
          conflict: policy,
        })
        id = session.id
        localStorage.setItem(key, id)
      }
      uploadSession.value = id
      controller = new AbortController()
      while (offset < file.size) {
        const chunk = file.slice(offset, Math.min(offset + 8 * 1024 ** 2, file.size))
        const result = await api(
          '/files/transfer?' + new URLSearchParams({ id, offset: String(offset) }),
          {
            method: 'PUT',
            headers: { 'Content-Type': 'application/octet-stream' },
            body: chunk,
            signal: controller.signal,
          },
        )
        offset = result.offset
        uploadProgress.value = Math.round((offset / Math.max(file.size, 1)) * 100)
      }
      await op('upload-finish', { id })
      localStorage.removeItem(key)
      uploadSession.value = ''
      uploadProgress.value = 100
      await load()
    }
    ElMessage.success('上传完成')
  } catch (e) {
    if ((e as Error)?.name === 'AbortError') {
      uploadPaused.value = true
      ElMessage.info('上传已暂停，重新选择同一文件可续传')
    } else showError(e)
  } finally {
    uploadBusy.value = false
    if (uploadInput.value) uploadInput.value.value = ''
  }
}
function pauseUpload() {
  controller?.abort()
}
async function discardUpload() {
  try {
    controller?.abort()
    if (uploadSession.value) {
      await op('upload-cancel', { id: uploadSession.value })
      for (const key of Object.keys(localStorage)) {
        if (key.startsWith('opsview:upload:') && localStorage.getItem(key) === uploadSession.value)
          localStorage.removeItem(key)
      }
      uploadSession.value = ''
      uploadPaused.value = false
      ElMessage.info('临时上传已清理')
    }
  } catch (e) {
    showError(e)
  }
}
onMounted(async () => {
  path.value = String(route.query.path || path.value)
  address.value = path.value
  await Promise.all([load(), refreshMeta()])
})
onUnmounted(() => {
  alive = false
  listing?.abort()
  metadataController.abort()
  controller?.abort()
})
</script>
<template>
  <ListPanel :busy="busy" :error="error" @retry="load">
    <div class="file-navigation">
      <el-button :icon="ArrowLeft" :disabled="path === '/'" @click="navigate(parent(path))">
        上级
      </el-button>
      <el-input v-model="address" aria-label="服务器路径" @keyup.enter="navigate(address)" />
      <el-button @click="navigate(address)">前往</el-button>
      <el-button :icon="RefreshRight" aria-label="刷新目录" title="刷新目录" @click="load" />
      <el-dropdown @command="navigate">
        <el-button>快捷目录</el-button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item v-for="s in metadata.shortcuts || []" :key="s.path" :command="s.path">
              {{ s.name }}
            </el-dropdown-item>
            <el-dropdown-item v-for="p in metadata.favorites || []" :key="p" :command="p">
              ★ {{ p }}
            </el-dropdown-item>
            <el-dropdown-item v-for="p in recent.slice(0, 5)" :key="'recent' + p" :command="p">
              最近 {{ p }}
            </el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>
    <div class="file-navigation">
      <el-breadcrumb>
        <el-breadcrumb-item><a @click="navigate('/')">根目录</a></el-breadcrumb-item>
        <el-breadcrumb-item v-for="part in breadcrumb" :key="part.path">
          <a @click="navigate(part.path)">{{ part.name }}</a>
        </el-breadcrumb-item>
      </el-breadcrumb>
      <el-button v-if="metadata.favorites?.includes(path)" link @click="favorite(path, true)">
        取消收藏
      </el-button>
    </div>
    <div class="file-actions">
      <el-button :disabled="readOnly || uploadBusy" type="primary" @click="uploadInput?.click()">
        上传文件
      </el-button>
      <el-button :disabled="readOnly" @click="create(false)">新建文件</el-button>
      <el-button :disabled="readOnly" @click="create(true)">新建目录</el-button>
      <el-button
        :disabled="!selection.length || readOnly"
        @click="clipboard = { paths: selection.map((r) => r.path), action: 'copy' }"
      >
        复制
      </el-button>
      <el-button
        :disabled="!selection.length || readOnly"
        @click="clipboard = { paths: selection.map((r) => r.path), action: 'move' }"
      >
        剪切
      </el-button>
      <el-button :disabled="!clipboard || readOnly || taskBusy" @click="paste">
        粘贴 {{ clipboard?.paths.length || '' }}
      </el-button>
      <el-dropdown>
        <el-button>更多操作</el-button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item :disabled="!selection.length || readOnly" @click="permissions()">
              修改权限
            </el-dropdown-item>
            <el-dropdown-item
              :disabled="!selection.length || readOnly || taskBusy"
              @click="archive('compress')"
            >
              压缩
            </el-dropdown-item>
            <el-dropdown-item :disabled="taskBusy || readOnly" @click="calculate()">
              计算大小
            </el-dropdown-item>
            <el-dropdown-item @click="favorite(path)">收藏当前目录</el-dropdown-item>
            <el-dropdown-item @click="loadTrash">回收站</el-dropdown-item>
            <el-dropdown-item
              :disabled="!selection.length || readOnly"
              divided
              @click="batch('delete')"
            >
              删除所选项目
            </el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>
    <input ref="uploadInput" type="file" multiple hidden @change="uploadFiles" />
    <div v-if="uploadBusy || uploadPaused" class="file-toolbar-filter">
      <span>{{ uploadName }}</span>
      <el-progress :percentage="uploadProgress" style="width: 200px" />
      <el-button v-if="uploadBusy" @click="pauseUpload">暂停</el-button>
      <el-button @click="discardUpload">取消并清理</el-button>
      <span v-if="uploadPaused">重新选择相同文件可续传</span>
    </div>
    <div v-if="taskBusy" class="file-toolbar-filter">
      <span>任务执行中，可在任务中心查看进度</span>
      <el-button @click="cancelTask">取消任务</el-button>
    </div>
    <div class="file-toolbar-filter">
      <el-input
        v-model="filter"
        placeholder="搜索文件 / 目录"
        clearable
        @keyup.enter="
          () => {
            page = 1
            load()
          }
        "
      />
      <el-button
        @click="
          () => {
            page = 1
            load()
          }
        "
      >
        搜索本目录
      </el-button>
      <el-button :disabled="taskBusy || readOnly" @click="search(false)">搜索子目录</el-button>
      <el-button :disabled="taskBusy || readOnly" @click="search(true)">内容搜索</el-button>
      <el-checkbox
        v-model="hidden"
        @change="
          () => {
            page = 1
            load()
          }
        "
      >
        显示隐藏文件
      </el-checkbox>
    </div>
    <div class="data-list">
      <el-table
        :data="items"
        row-key="path"
        @selection-change="selection = $event"
        @sort-change="
          ($event: any) => {
            sort = $event.prop || 'name'
            descending = $event.order === 'descending'
            load()
          }
        "
      >
        <el-table-column
          type="selection"
          width="48"
          :selectable="(r: Config) => !r.protected && !r.special"
        />
        <el-table-column label="文件名称" prop="name" min-width="250" sortable="custom">
          <template #default="{ row }">
            <el-button link class="file-name" :title="row.name" @click="open(row)">
              <el-icon>
                <Folder v-if="row.directory || row.symlink" />
                <Document v-else />
              </el-icon>
              <span>{{ row.name }}{{ row.symlink ? ' → ' + row.target : '' }}</span>
            </el-button>
          </template>
        </el-table-column>
        <el-table-column label="权限 / 所有者" min-width="170">
          <template #default="{ row }">
            <el-button link :disabled="row.protected" @click="permissions([row])">
              {{ row.mode }} / {{ row.owner }}:{{ row.group }}
            </el-button>
          </template>
        </el-table-column>
        <el-table-column label="大小" prop="size" width="110" sortable="custom">
          <template #default="{ row }">
            <el-button
              v-if="row.directory"
              link
              :disabled="row.protected || taskBusy"
              @click="calculate(row)"
            >
              计算
            </el-button>
            <span v-else>{{ size(row.size) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="修改时间" prop="modifiedAt" min-width="175" sortable="custom">
          <template #default="{ row }">{{ date(row.modifiedAt) }}</template>
        </el-table-column>
        <el-table-column label="备注" min-width="120" show-overflow-tooltip>
          <template #default="{ row }">
            <el-button link :disabled="row.protected" @click="note(row)">
              {{ row.note || '添加备注' }}
            </el-button>
          </template>
        </el-table-column>
        <el-table-column label="操作" fixed="right" width="230">
          <template #default="{ row }">
            <el-button
              v-if="!row.directory && !row.symlink"
              link
              type="primary"
              :disabled="row.protected || row.special"
              @click="edit(row.path)"
            >
              编辑
            </el-button>
            <el-button
              v-if="!row.directory"
              link
              type="primary"
              :disabled="row.protected || row.special"
              @click="download(row)"
            >
              下载
            </el-button>
            <el-dropdown
              @command="
                (cmd: string) =>
                  cmd === 'rename'
                    ? rename(row)
                    : cmd === 'extract'
                      ? archive('extract', row)
                      : cmd === 'permissions'
                        ? permissions([row])
                        : batch('delete', [row])
              "
            >
              <el-button link :disabled="row.protected || row.special">更多</el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="rename">重命名</el-dropdown-item>
                  <el-dropdown-item command="permissions">权限</el-dropdown-item>
                  <el-dropdown-item v-if="/\.(zip|tar\.gz)$/i.test(row.name)" command="extract">
                    解压
                  </el-dropdown-item>
                  <el-dropdown-item command="delete" divided>移入回收站</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </el-table-column>
      </el-table>
    </div>
    <template #footer>
      <div class="list-pagination">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 50, 100, 500]"
          layout="total,sizes,prev,pager,next"
          @current-change="load"
          @size-change="
            () => {
              page = 1
              load()
            }
          "
        />
      </div>
      <div class="file-path-note">{{ path }} · 上传上限 2 GiB · 删除默认保留 30 天</div>
    </template>
  </ListPanel>
  <el-dialog
    v-model="editOpen"
    :title="'编辑 ' + editPath"
    width="85%"
    :close-on-click-modal="false"
    destroy-on-close
  >
    <p class="form-help">UTF-8 文本 · Ctrl/⌘ F 搜索 · 保存时检查外部修改</p>
    <CodeEditor v-model="content" />
    <template #footer>
      <el-button @click="edit(editPath)">重新载入</el-button>
      <el-button @click="editOpen = false">取消</el-button>
      <el-button type="primary" @click="diffOpen = true">查看差异并保存</el-button>
    </template>
  </el-dialog>
  <el-dialog v-model="diffOpen" title="确认文件修改" width="75%">
    <pre class="log-box">{{ changes }}</pre>
    <template #footer>
      <el-button @click="diffOpen = false">返回编辑</el-button>
      <el-button type="primary" :loading="saving" @click="save">保存</el-button>
    </template>
  </el-dialog>
  <el-dialog v-model="permissionOpen" title="权限与所有者" width="540px">
    <el-form label-position="top">
      <el-form-item label="八进制权限（0000–0777）">
        <el-input v-model="permission.mode" />
      </el-form-item>
      <el-form-item label="所有者"><el-input v-model="permission.owner" /></el-form-item>
      <el-form-item label="用户组"><el-input v-model="permission.group" /></el-form-item>
      <el-checkbox v-model="permission.recursive" @change="permissionCount = ''">
        递归应用到子目录和文件
      </el-checkbox>
      <el-button v-if="permission.recursive" :disabled="taskBusy" @click="permissionPreview">
        计算影响范围
      </el-button>
      <p>{{ permissionCount }}</p>
    </el-form>
    <template #footer>
      <el-button @click="permissionOpen = false">取消</el-button>
      <el-button type="primary" @click="savePermissions">应用</el-button>
    </template>
  </el-dialog>
  <el-dialog v-model="trashOpen" title="文件回收站" width="85%">
    <div class="page-toolbar">
      <p>默认保留 30 天，恢复时不会覆盖已有文件。</p>
      <el-button type="danger" @click="trashAction('empty')">清空回收站</el-button>
    </div>
    <DataList state-key="Files-1" :rows="trash" searchable>
      <el-table-column prop="original" label="原路径" min-width="260" show-overflow-tooltip />
      <el-table-column label="删除时间" min-width="170">
        <template #default="{ row }">{{ date(row.deletedAt) }}</template>
      </el-table-column>
      <el-table-column label="到期时间" min-width="170">
        <template #default="{ row }">{{ date(row.expiresAt) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="180">
        <template #default="{ row }">
          <el-button link type="primary" @click="trashAction('restore', row)">恢复</el-button>
          <el-button link type="danger" @click="trashAction('purge', row)">永久删除</el-button>
        </template>
      </el-table-column>
    </DataList>
  </el-dialog>
  <el-dialog v-model="resultOpen" :title="resultTitle" width="85%">
    <DataList
      state-key="Files-2"
      v-if="taskResult?.items?.length && typeof taskResult.items[0] === 'object'"
      :rows="taskResult.items"
    >
      <el-table-column label="路径" min-width="300">
        <template #default="{ row }">
          <el-button
            link
            @click="
              () => {
                resultOpen = false
                navigate(parent(row.path))
              }
            "
          >
            {{ row.path }}
          </el-button>
        </template>
      </el-table-column>
      <el-table-column label="结果">
        <template #default="{ row }">
          {{ row.skipped ? '同名跳过' : row.target || row.name || '完成' }}
        </template>
      </el-table-column>
    </DataList>
    <pre v-else class="log-box">{{ resultJSON }}</pre>
  </el-dialog>
</template>
