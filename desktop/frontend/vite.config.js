import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Wails 会把 frontend/dist 整个 embed 进 exe。
//
// server.host 固定为 127.0.0.1：开发期只监听回环，
// 避免 Windows 防火墙每次启动都弹「是否允许访问网络」的提示。
export default defineConfig({
  plugins: [vue()],
  server: {
    host: '127.0.0.1',
    port: 34115,
    strictPort: false,
  },
  build: {
    // 桌面端本地加载，不需要考虑公共 CDN 兼容性
    target: 'chrome110',
    chunkSizeWarningLimit: 2000,
  },
})
