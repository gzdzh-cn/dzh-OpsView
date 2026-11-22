<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, post, action, getCSRF, date, size, errorText, type Config } from '../lib/api'
import { usePanel } from '../stores/panel'
import LoadingContent from './LoadingContent.vue'
import DataList from './DataList.vue'
import AppDialog from './AppDialog.vue'
import Resources from '../views/Resources.vue'
const props = defineProps<{ panelOnly?: boolean }>(),
  panel = usePanel(),
  rows = ref<Config[]>([]),
  busy = ref(true),
  hasData = ref(false),
  error = ref(''),
  detail = ref<Config>(),
  open = ref(false),
  upload = ref(false),
  file = ref<File>(),
  progress = ref(0),
  uploading = ref(false),
  backupId = ref(''),
  request = ref<XMLHttpRequest>()
let sequence = 0
const backupKinds: Record<string, string> = {
  panel: '面板',
  site: '网站',
  database: '数据库',
  directory: '目录',
  instance: '实例',
  app: '应用',
}
const reads = new AbortController()
onBeforeUnmount(() => {
  sequence++
  reads.abort()
  request.value?.abort()
})
async function load() {
  const seq = ++sequence
  busy.value = !hasData.value
  error.value = ''
  try {
    const metadata = await api<Config[]>('/settings/backups', { signal: reads.signal })
    if (seq !== sequence) return
    hasData.value = true
    rows.value = metadata.map((r) => ({
      ...r,
      file: r.name,
      kind: 'panel',
      state: 'succeeded',
      targetName: '面板配置',
      url: r.url,
    }))
    if (!props.panelOnly) {
      try {
        const result = await api('/backups/archives', { signal: reads.signal })
        if (seq !== sequence) return
        rows.value.push(
          ...result.rows.map((r: Config) => ({
            ...r,
            kind: r.targetKind,
            state: 'succeeded',
            targetName:
              panel.resources.find((x) => x.id === r.targetId)?.name || r.sourcePath || r.targetId,
            url:
              '/api/v1/backups/download?backupId=' +
              encodeURIComponent(r.backupId) +
              '&file=' +
              encodeURIComponent(r.file),
          })),
        )
      } catch (e) {
        error.value = errorText(e)
      }
    }
  } catch (e) {
    error.value = errorText(e)
  } finally {
    if (seq === sequence) busy.value = false
  }
}
onMounted(load)
async function backup() {
  try {
    await post('/settings/backup')
    ElMessage.success('备份任务已提交，完成后可在此查看')
    await panel.refresh(['tasks'])
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function archiveTask(verb: string, row: Config) {
  row.working = true
  try {
    const task = await post('/backups/archives/' + verb, {
      kind: row.kind,
      file: row.file,
      backupId: row.backupId,
    })
    for (;;) {
      const result = await api('/backups/tasks/' + task.id, { signal: reads.signal })
      if (result.state === 'succeeded') return result.result
      if (['failed', 'cancelled', 'interrupted'].includes(result.state))
        throw new Error(result.error || '任务未完成')
      await new Promise<void>((resolve, reject) => {
        const stop = () => {
          clearTimeout(timer)
          reject(new Error('页面已关闭'))
        }
        const timer = setTimeout(() => {
          reads.signal.removeEventListener('abort', stop)
          resolve()
        }, 700)
        reads.signal.addEventListener('abort', stop, { once: true })
      })
    }
  } finally {
    row.working = false
  }
}
async function remove(row: Config) {
  try {
    await ElMessageBox.confirm('永久删除此备份文件？恢复和校验中的归档不能删除。', '删除备份', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
    await archiveTask('delete', row)
    await load()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close' && !reads.signal.aborted) ElMessage.error(errorText(e))
  }
}
async function preview(row: Config) {
  try {
    detail.value = { ...row, ...(await archiveTask('preview', row)) }
    open.value = true
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function verify(row: Config) {
  try {
    await archiveTask('verify', row)
    row.verified = true
    ElMessage.success('备份已验证')
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function restore() {
  if (!detail.value) return
  try {
    if (detail.value.kind === 'directory') {
      const { value } = await ElMessageBox.prompt(
        '填写尚不存在的新目录，原目录不会覆盖',
        '恢复目录',
        {
          inputPlaceholder: '/www/restores/example',
          confirmButtonText: '恢复',
          cancelButtonText: '取消',
        },
      )
      await post('/backups/archives/restore-directory', {
        backupId: detail.value.backupId,
        file: detail.value.file,
        destination: value,
      })
    } else {
      const backup = panel.resources.find(
        (r) => r.id === detail.value!.backupId && r.kind === 'backup',
      )
      if (!backup) throw new Error('请先恢复原备份配置及软件依赖，再从业务备份入口恢复')
      const target = panel.resources.find((r) => r.id === backup.config.targetId)
      const params: Config = { artifact: detail.value.file }
      if (target?.kind === 'database')
        params.databaseName = (
          await ElMessageBox.prompt('填写新数据库名，拒绝覆盖已有库', '恢复数据库', {
            confirmButtonText: '恢复',
            cancelButtonText: '取消',
          })
        ).value
      await action(backup.id, 'restore', params)
    }
    ElMessage.success('恢复任务已提交')
    open.value = false
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
function selected(event: Event) {
  file.value = (event.target as HTMLInputElement).files?.[0]
}
function cancelUpload() {
  request.value?.abort()
  upload.value = false
}
async function send() {
  if (!file.value || uploading.value) return
  uploading.value = true
  progress.value = 0
  try {
    const xhr = new XMLHttpRequest()
    request.value = xhr
    await new Promise<void>((resolve, reject) => {
      xhr.open(
        'PUT',
        '/api/v1/backups/upload?file=' +
          encodeURIComponent(file.value!.name) +
          '&backupId=' +
          encodeURIComponent(backupId.value),
      )
      xhr.setRequestHeader('X-CSRF-Token', getCSRF())
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) progress.value = Math.round((e.loaded / e.total) * 100)
      }
      xhr.onload = () =>
        xhr.status < 300
          ? resolve()
          : reject(new Error(JSON.parse(xhr.responseText).error || '上传失败'))
      xhr.onerror = () => reject(new Error('上传失败'))
      xhr.onabort = () => reject(new Error('上传已取消'))
      xhr.send(file.value!)
    })
    upload.value = false
    ElMessage.success('归档上传并校验完成')
    await load()
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    uploading.value = false
    request.value = undefined
  }
}
</script>
<template>
  <section class="surface">
    <div class="page-toolbar">
      <div class="page-toolbar-left">
        <el-button type="primary" @click="backup">备份面板</el-button>
        <el-button @click="upload = true">上传归档</el-button>
        <el-button @click="load">刷新记录</el-button>
      </div>
    </div>
    <el-alert v-if="error" :title="error" type="warning" :closable="false">
      <el-button link @click="load">重试</el-button>
    </el-alert>
    <LoadingContent :busy="busy" :ready="hasData">
      <DataList :rows="rows" state-key="backup-records" searchable>
        <el-table-column prop="targetName" label="对象" min-width="130" />
        <el-table-column label="类型" width="100">
          <template #default="{ row }">{{ backupKinds[row.kind] || row.kind }}</template>
        </el-table-column>
        <el-table-column label="备份文件" min-width="220">
          <template #default="{ row }">
            <a :href="row.url">{{ row.file }}</a>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">{{ row.working ? '处理中' : '成功' }}</template>
        </el-table-column>
        <el-table-column prop="storage" label="存储位置" min-width="180" show-overflow-tooltip />
        <el-table-column label="大小" width="110">
          <template #default="{ row }">{{ size(row.size) }}</template>
        </el-table-column>
        <el-table-column label="校验" width="95">
          <template #default="{ row }">{{ row.verified ? '已校验' : '待校验' }}</template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="165">
          <template #default="{ row }">{{ date(row.createdAt) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="240" fixed="right">
          <template #default="{ row }">
            <div class="row-actions">
              <a :href="row.url">下载</a>
              <button :disabled="row.working" @click="verify(row)">校验</button>
              <button :disabled="row.working" @click="preview(row)">恢复核对</button>
              <button class="danger" :disabled="row.working" @click="remove(row)">删除</button>
            </div>
          </template>
        </el-table-column>
      </DataList>
    </LoadingContent>
  </section>
  <details v-if="!panelOnly" class="surface compact-details">
    <summary>业务备份配置与存储</summary>
    <Resources kind="backup" embedded />
  </details>
  <AppDialog v-model="open" title="备份恢复核对" width="700px">
    <el-descriptions v-if="detail" :column="1" border>
      <el-descriptions-item label="文件">{{ detail.file }}</el-descriptions-item>
      <el-descriptions-item label="目标">{{ detail.targetName }}</el-descriptions-item>
      <el-descriptions-item label="校验">
        {{ detail.verified ? '通过' : '待校验' }}
      </el-descriptions-item>
      <el-descriptions-item label="恢复方式">恢复到新目录、新库或新实例</el-descriptions-item>
      <el-descriptions-item label="所需密钥">
        {{ detail.requiresKey || '执行器 backup.key' }}
      </el-descriptions-item>
    </el-descriptions>
    <p class="form-help">
      核对版本、空间和依赖后执行。在线备份不代表零数据损失，原有数据不会被覆盖。
    </p>
    <pre v-if="detail?.command" class="log-box">{{ detail.command }}</pre>
    <template #footer>
      <el-button @click="open = false">关闭</el-button>
      <el-button v-if="detail?.kind !== 'panel'" type="primary" @click="restore">
        恢复至新目标
      </el-button>
    </template>
  </AppDialog>
  <AppDialog v-model="upload" title="上传 OpsView 备份" width="650px" :close-on-click-modal="false">
    <p>支持 .ovp、.ovb，单文件不超过 2 GiB。主密钥独立保管。.ovb 必须匹配本机受信任备份历史。</p>
    <input type="file" accept=".ovp,.ovb" aria-label="备份归档" @change="selected" />
    <el-form-item v-if="file?.name.endsWith('.ovb')" label="原备份配置">
      <el-select v-model="backupId">
        <el-option
          v-for="r in panel.resources.filter((r) => r.kind === 'backup' || r.kind === 'schedule')"
          :key="r.id"
          :label="r.name"
          :value="r.id"
        />
      </el-select>
    </el-form-item>
    <el-progress v-if="uploading" :percentage="progress" />
    <template #footer>
      <el-button @click="cancelUpload">
        {{ uploading ? '取消上传' : '取消' }}
      </el-button>
      <el-button type="primary" :disabled="!file" :loading="uploading" @click="send">
        上传并核对
      </el-button>
    </template>
  </AppDialog>
</template>
