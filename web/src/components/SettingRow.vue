<script setup lang="ts">
defineProps<{
  label: string
  value?: string | number
  help?: string
  editing?: boolean
  busy?: boolean
  disabled?: boolean
}>()
defineEmits<{ edit: []; save: []; cancel: [] }>()
</script>
<template>
  <div class="preference-item">
    <div class="preference-row">
      <strong>{{ label }}</strong>
      <div class="preference-value">
        <slot v-if="editing" name="editor" />
        <slot v-else>
          <button
            class="preference-value-button"
            :disabled="disabled"
            :aria-label="'修改' + label"
            @click="$emit('edit')"
          >
            {{ value ?? '—' }}
          </button>
        </slot>
        <small v-if="help">{{ help }}</small>
      </div>
      <div class="preference-action">
        <template v-if="editing">
          <el-button link :disabled="busy" @click="$emit('cancel')">取消</el-button>
          <el-button type="primary" :loading="busy" :disabled="disabled" @click="$emit('save')">
            保存
          </el-button>
        </template>
        <slot v-else name="action">
          <el-button link type="primary" :disabled="disabled" @click="$emit('edit')">
            修改
          </el-button>
        </slot>
      </div>
    </div>
    <slot name="details" />
  </div>
</template>
