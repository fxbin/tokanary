import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 静态仪表盘：无 SSR/路由/状态库。数据不打包进产物——窗口在运行时
// fetch('/api/dashboard')，直读仓库 SQLite；构建产物里只有壳。
// base './' 让 dist 可 file:// 双击，也可托管在任意子路径。
export default defineConfig({
  base: './',
  plugins: [vue()],
  publicDir: 'public',
  build: {
    outDir: 'dist',
    emptyOutDir: true
  }
})
