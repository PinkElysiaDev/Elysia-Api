import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react-swc'
import path from 'node:path'

// Backend serves the built assets from gin.Static("/ui", webuiDir),
// so the production base must be "/ui/". During dev we proxy admin/relay
// calls to a locally running backend (default 127.0.0.1:8765).
export default defineConfig(({ command }) => ({
  base: command === 'build' ? '/ui/' : '/',
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  server: {
    port: 5273,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8765', changeOrigin: true },
      '/v1': { target: 'http://127.0.0.1:8765', changeOrigin: true },
      '/health': { target: 'http://127.0.0.1:8765', changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1200,
  },
}))
