<script setup lang="ts">
import { computed, watch, useAttrs, ref, shallowRef, onMounted } from 'vue'
import { usePageState } from '../composables/pageState'
defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{
    rows: any[]
    stateKey?: string
    searchable?: boolean
    pageSize?: number
    emptyText?: string
  }>(),
  { pageSize: 20, emptyText: '暂无数据' },
)
const host = ref<HTMLElement>(),
  toolbar = shallowRef<Element | null>(null)
onMounted(() => {
  const surface = host.value?.closest('.surface')
  toolbar.value =
    surface?.querySelector(':scope > .page-toolbar .page-toolbar-left') ||
    surface?.querySelector(':scope > .page-toolbar') ||
    null
})
const attrs = useAttrs(),
  query = usePageState(() => `${props.stateKey}:query`, ''),
  page = usePageState(() => `${props.stateKey}:page`, 1),
  limit = usePageState(() => `${props.stateKey}:limit`, props.pageSize)
const filtered = computed(() =>
  !query.value
    ? props.rows
    : props.rows.filter((row) =>
        Object.values(row).some(
          (v) => typeof v === 'string' && v.toLowerCase().includes(query.value.toLowerCase()),
        ),
      ),
)
const visible = computed(() =>
  filtered.value.slice((page.value - 1) * limit.value, page.value * limit.value),
)
watch([query, limit], () => (page.value = 1))
watch(
  () => filtered.value.length,
  () => {
    if (!filtered.value.length) return
    page.value = Math.min(page.value, Math.max(1, Math.ceil(filtered.value.length / limit.value)))
  },
)
</script>
<template>
  <div ref="host" class="data-list">
    <Teleport v-if="searchable" :to="toolbar || 'body'" :disabled="!toolbar">
      <div class="list-filter">
        <el-input v-model="query" clearable placeholder="搜索列表" aria-label="搜索列表" />
      </div>
    </Teleport>
    <el-table v-bind="attrs" :data="visible" :empty-text="emptyText"><slot /></el-table>
    <div class="list-pagination">
      <el-pagination
        v-model:current-page="page"
        v-model:page-size="limit"
        :page-sizes="[20, 50, 100]"
        :total="filtered.length"
        layout="total, sizes, prev, pager, next"
        small
      />
    </div>
  </div>
</template>
