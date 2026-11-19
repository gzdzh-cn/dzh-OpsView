<script setup lang="ts">
import { provide, ref, useSlots } from 'vue'
import { ElDialog } from 'element-plus'
import { dialogActionsKey } from '../lib/dialog'

defineOptions({ inheritAttrs: false })
withDefaults(defineProps<{ size?: 'default' | 'small'; split?: boolean }>(), { size: 'default' })
const dialog = ref<InstanceType<typeof ElDialog>>()
const actions = ref<HTMLElement>()
const slots = useSlots()
provide(dialogActionsKey, actions)
</script>

<template>
  <el-dialog
    ref="dialog"
    v-bind="$attrs"
    class="ops-dialog"
    :class="[`ops-dialog--${size}`, { 'ops-dialog--split': split }]"
    align-center
    append-to-body
  >
    <template
      v-for="name in Object.keys(slots).filter((name) => name !== 'footer')"
      #[name]="scope"
    >
      <slot :name="name" v-bind="scope || {}" />
    </template>
    <template #footer>
      <div class="ops-dialog-footer">
        <el-button v-if="!slots.footer" @click="dialog?.handleClose()">关闭</el-button>
        <div ref="actions" class="ops-dialog-action-target" />
        <slot name="footer" />
      </div>
    </template>
  </el-dialog>
</template>
