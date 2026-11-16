import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig({
  plugins: [vue()],
  server: { proxy: { '/api': { target: 'http://127.0.0.1:18790', changeOrigin: false } } },
  build: {
    chunkSizeWarningLimit: 1800,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules/element-plus')) return 'ui'
          if (id.includes('node_modules/echarts') || id.includes('node_modules/zrender'))
            return 'charts'
        },
      },
    },
  },
})
