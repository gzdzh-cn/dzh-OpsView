<script setup lang="ts">
import { computed, ref } from 'vue'
import { usePageQuery } from '../composables/pageQuery'
import CodeEditor from './CodeEditor.vue'
import type { Config } from '../lib/api'
const config = defineModel<Config>({ required: true })
const props = defineProps<{ siteId: string }>()
const query = usePageQuery<Config>(() => `/sites/${props.siteId}/configuration`, {}, 0)
const revision = ref('current')
const content = computed(() =>
  revision.value === 'current'
    ? query.data.value.current || ''
    : (query.data.value.history || []).find((v: Config) => v.revision === revision.value)
        ?.content || '',
)
</script>
<template>
  <el-alert v-if="query.error.value" :title="query.error.value" type="error" :closable="false" />
  <el-alert
    v-if="query.data.value.drift"
    title="检测到面板外配置变更，请先核对实际配置；保存不会覆盖未知变更。"
    type="warning"
    :closable="false"
  />
  <div class="page-toolbar">
    <el-select v-model="revision" aria-label="配置版本">
      <el-option value="current" label="当前实际配置" />
      <el-option
        v-for="item in query.data.value.history || []"
        :key="item.revision"
        :value="item.revision"
        :label="item.revision"
      />
    </el-select>
    <el-button :loading="query.busy.value" @click="query.load(true)">刷新配置</el-button>
  </div>
  <CodeEditor :key="revision" :model-value="content" readonly />
  <el-form label-position="top">
    <el-form-item label="受控高级参数">
      <CodeEditor
        :model-value="config.advanced || ''"
        @update:model-value="config.advanced = $event"
      />
    </el-form-item>
  </el-form>
  <p class="form-help">
    完整配置及历史供核对。通过各设置分区管理结构化规则；此处只接受经过校验的 server 级参数，禁止任意
    include、文件读取及指令注入。保存前检查全部 Nginx 配置，失败保留原配置。
  </p>
</template>
