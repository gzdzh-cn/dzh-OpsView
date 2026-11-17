<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import type { EditorView as Editor } from '@codemirror/view'
const props = defineProps<{ modelValue: string; readonly?: boolean }>(),
  emit = defineEmits(['update:modelValue']),
  el = ref<HTMLElement>()
let editor: Editor
let alive = true
onMounted(async () => {
  const [{ EditorView }, { basicSetup }] = await Promise.all([
    import('@codemirror/view'),
    import('codemirror'),
  ])
  if (!alive || !el.value) return
  editor = new EditorView({
    doc: props.modelValue,
    parent: el.value,
    extensions: [
      basicSetup,
      EditorView.editable.of(!props.readonly),
      EditorView.lineWrapping,
      EditorView.theme({
        '&': { fontSize: '13px', minHeight: '280px' },
        '.cm-scroller': {
          fontFamily: 'var(--font-mono)',
          minHeight: '280px',
          maxHeight: '480px',
          overflow: 'auto',
        },
        '.cm-gutters': { backgroundColor: '#f7f9f8', border: 'none' },
      }),
      EditorView.updateListener.of((v) => {
        if (v.docChanged) emit('update:modelValue', v.state.doc.toString())
      }),
    ],
  })
})
watch(
  () => props.modelValue,
  (v) => {
    if (editor && v !== editor.state.doc.toString())
      editor.dispatch({ changes: { from: 0, to: editor.state.doc.length, insert: v } })
  },
)
onUnmounted(() => {
  alive = false
  editor?.destroy()
})
</script>
<template><div ref="el" class="code-editor"></div></template>
