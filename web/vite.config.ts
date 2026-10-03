import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 静态仪表盘:无 SSR/路由/状态库;data.js 由 predev/prebuild 从仓根同步进 public/。
// base './' 让 dist 可 file:// 双击,也可托管在任意子路径。
export default defineConfig({
  base: './',
  plugins: [vue()],
  publicDir: 'public',
  build: {
    outDir: 'dist',
    emptyOutDir: true
  }
})
