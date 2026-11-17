<script setup lang="ts">
import { computed, ref, watch, useAttrs } from 'vue'
defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{ rows: any[]; searchable?: boolean; pageSize?: number; emptyText?: string }>(),
  { pageSize: 20, emptyText: '暂无数据' },
)
const attrs = useAttrs(),
  query = ref(''),
  page = ref(1),
  limit = ref(props.pageSize)
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
    page.value = Math.min(page.value, Math.max(1, Math.ceil(filtered.value.length / limit.value)))
  },
)
</script>
<template>
  <div class="data-list">
    <div v-if="searchable" class="list-filter">
      <el-input v-model="query" clearable placeholder="搜索列表" aria-label="搜索列表" />
    </div>
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
