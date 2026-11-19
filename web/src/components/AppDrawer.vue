<script setup lang="ts">
import { ref, useSlots } from 'vue'
import { ElDrawer } from 'element-plus'

defineOptions({ inheritAttrs: false })
const drawer = ref<InstanceType<typeof ElDrawer>>()
const slots = useSlots()
</script>

<template>
  <el-drawer ref="drawer" v-bind="$attrs" class="ops-drawer" append-to-body>
    <template
      v-for="name in Object.keys(slots).filter((name) => name !== 'footer')"
      #[name]="scope"
    >
      <slot :name="name" v-bind="scope || {}" />
    </template>
    <template #footer>
      <div class="ops-dialog-footer">
        <slot name="footer"><el-button @click="drawer?.handleClose()">关闭</el-button></slot>
      </div>
    </template>
  </el-drawer>
</template>
