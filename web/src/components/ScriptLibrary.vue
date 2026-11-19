<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, post, put, errorText, type Config, type Resource } from '../lib/api'
import { saveJSON } from '../lib/identity'
import { usePageQuery } from '../composables/pageQuery'
import DataList from './DataList.vue'
import AppDialog from './AppDialog.vue'
import LoadingContent from './LoadingContent.vue'
import ScheduleEditor from './ScheduleEditor.vue'
const { data, busy, error, load } = usePageQuery<Resource[]>('/scripts', [])
const history = ref<Config[]>([]),
  version = ref<number>()
async function loadVersion(n: number) {
  if (!editing.value) return
  try {
    const r = await api<Resource>('/scripts/' + editing.value.id + '?revision=' + n)
    config.value = { ...r.config }
  } catch (e) {
    failure.value = errorText(e)
  }
}
const open = ref(false),
  editing = ref<Resource>(),
  name = ref(''),
  config = ref<Config>({}),
  saving = ref(false),
  failure = ref(''),
  scheduleOpen = ref(false),
  initial = ref<Config>(),
  importOpen = ref(false),
  importText = ref('')
watch(open, (value) => {
  if (!value) {
    config.value = {}
    editing.value = undefined
    history.value = []
  }
})
watch(importOpen, (value) => {
  if (!value) importText.value = ''
})
watch(scheduleOpen, (value) => {
  if (!value) initial.value = undefined
})
onBeforeUnmount(() => {
  config.value = {}
  initial.value = undefined
  importText.value = ''
})
async function edit(row?: Resource) {
  failure.value = ''
  try {
    const r = row ? await api<Resource>('/scripts/' + row.id) : undefined
    editing.value = r
    name.value = r?.name || ''
    config.value = {
      operation: 'shell',
      script: '',
      category: '默认分类',
      interpreter: '/usr/bin/python3',
      ...r?.config,
    }
    history.value = r ? await api('/scripts/' + r.id + '?history=1') : []
    version.value = r?.revision
    open.value = true
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function save() {
  saving.value = true
  try {
    const body = { name: name.value, config: config.value, revision: editing.value?.revision }
    if (editing.value) await put('/scripts/' + editing.value.id, body)
    else await post('/scripts', body)
    open.value = false
    config.value = {}
    await load(true)
  } catch (e) {
    failure.value = errorText(e)
  } finally {
    saving.value = false
  }
}
async function remove(row: Resource) {
  try {
    await ElMessageBox.confirm('删除脚本模板？已有计划使用自己的副本，继续保留。', '删除脚本', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
    await api('/scripts/' + row.id, { method: 'DELETE' })
    await load(true)
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') ElMessage.error(errorText(e))
  }
}
async function createSchedule(row: Resource) {
  try {
    const r = await api<Resource>('/scripts/' + row.id)
    initial.value = { ...r.config, enabled: false }
    scheduleOpen.value = true
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function exportScript(row: Resource) {
  try {
    const r = await api<Resource>('/scripts/' + row.id)
    saveJSON(
      {
        format: 'opsview-script',
        version: 1,
        name: r.name,
        config: {
          operation: r.config.operation,
          script: r.config.script,
          category: r.config.category,
          interpreter: r.config.interpreter,
        },
      },
      r.name + '.json',
    )
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
async function importScript() {
  try {
    const r = JSON.parse(importText.value)
    if (r.format !== 'opsview-script' || r.version !== 1) throw new Error('仅支持 OpsView 脚本格式')
    await post('/scripts', { name: r.name, config: r.config })
    importOpen.value = false
    importText.value = ''
    await load(true)
  } catch (e) {
    ElMessage.error(errorText(e))
  }
}
</script>
<template>
  <section class="surface">
    <div class="page-toolbar">
      <div class="page-toolbar-left">
        <el-button type="primary" @click="edit()">添加脚本</el-button>
        <el-button @click="importOpen = true">导入</el-button>
      </div>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false">
      <el-button link @click="load(true)">重试</el-button>
    </el-alert>
    <LoadingContent :busy="busy">
      <DataList :rows="data" state-key="script-library" searchable>
        <el-table-column prop="name" label="脚本名称" min-width="160" />
        <el-table-column label="类型">
          <template #default="{ row }">{{ row.config.operation }}</template>
        </el-table-column>
        <el-table-column label="分类">
          <template #default="{ row }">{{ row.config.category }}</template>
        </el-table-column>
        <el-table-column prop="revision" label="版本" width="90" />
        <el-table-column label="操作" min-width="240" fixed="right">
          <template #default="{ row }">
            <div class="row-actions">
              <button @click="createSchedule(row)">创建计划</button>
              <button @click="edit(row)">编辑</button>
              <button @click="exportScript(row)">导出</button>
              <button class="danger" @click="remove(row)">删除</button>
            </div>
          </template>
        </el-table-column>
      </DataList>
    </LoadingContent>
  </section>
  <AppDialog
    v-model="open"
    :title="editing ? '编辑脚本模板' : '添加脚本模板'"
    width="800px"
    destroy-on-close
  >
    <el-alert v-if="failure" :title="failure" type="error" :closable="false" />
    <el-form label-width="100px">
      <el-form-item v-if="history.length" label="历史版本">
        <el-select v-model="version" @change="loadVersion">
          <el-option
            v-for="v in history"
            :key="v.revision"
            :value="v.revision"
            :label="'版本 ' + v.revision + ' · ' + v.createdAt"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="名称"><el-input v-model="name" /></el-form-item>
      <el-form-item label="类型">
        <el-select v-model="config.operation">
          <el-option label="Shell" value="shell" />
          <el-option label="Python" value="python" />
        </el-select>
      </el-form-item>
      <el-form-item label="分类"><el-input v-model="config.category" /></el-form-item>
      <el-form-item v-if="config.operation === 'python'" label="解释器">
        <el-input v-model="config.interpreter" />
      </el-form-item>
      <el-form-item label="内容">
        <el-input v-model="config.script" type="textarea" :rows="16" spellcheck="false" />
      </el-form-item>
    </el-form>
    <p class="form-help">保存模板不会执行脚本，也不会修改已有计划。</p>
    <template #footer>
      <el-button @click="open = false">取消</el-button>
      <el-button type="primary" :loading="saving" @click="save">保存</el-button>
    </template>
  </AppDialog>
  <AppDialog v-model="importOpen" title="导入脚本模板" width="700px">
    <el-input v-model="importText" type="textarea" :rows="15" placeholder="OpsView 脚本 JSON" />
    <template #footer>
      <el-button @click="importOpen = false">取消</el-button>
      <el-button type="primary" @click="importScript">导入模板</el-button>
    </template>
  </AppDialog>
  <ScheduleEditor
    v-model="scheduleOpen"
    :initial="initial"
    @saved="ElMessage.success('已创建计划，请在计划任务中查看')"
  />
</template>
