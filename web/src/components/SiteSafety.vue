<script setup lang="ts">
import LoadingContent from './LoadingContent.vue'
import { usePageQuery } from '../composables/pageQuery'
import type { Config } from '../lib/api'
const props = defineProps<{ siteId: string }>()
const query = usePageQuery<Config>(() => `/sites/${props.siteId}/security`, {}, 0)
</script>
<template>
  <el-alert v-if="query.error.value" :title="query.error.value" type="error" :closable="false" />
  <div class="page-toolbar">
    <el-button :loading="query.busy.value" @click="query.load(true)">重新检查</el-button>
    <small class="form-help">本机安全基线检查</small>
  </div>
  <LoadingContent :busy="query.busy.value" :ready="query.hasData.value">
    <el-table :data="query.data.value.checks || []">
      <el-table-column prop="name" label="检查项" width="140" />
      <el-table-column prop="status" label="结果" width="90" />
      <el-table-column prop="detail" label="详情" min-width="180" />
    </el-table>
  </LoadingContent>
  <p class="form-help">
    检查配置、权限和常见敏感文件名；不读取文件内容，不代表完整漏洞扫描或恶意代码查杀。内核防篡改及专业扫描需要独立执行器，当前版本未集成。
  </p>
</template>
