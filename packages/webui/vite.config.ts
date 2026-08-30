import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react-swc'
import path from 'node:path'

// Backend serves the built assets from gin.Static("/ui", webuiDir),
// so the production base must be "/ui/". During dev we proxy admin/relay
// calls to a locally running backend (default 127.0.0.1:8765).
// Override the proxy target with ELYSIA_DEV_PROXY.
function devProxyTarget(): string {
  const raw = process.env.ELYSIA_DEV_PROXY?.trim()
  if (raw) return raw
  return 'http://127.0.0.1:8765'
}

function devServerHost(): string | boolean {
  const raw = process.env.ELYSIA_DEV_HOST?.trim()
  if (!raw) return '127.0.0.1'
  if (raw === '1' || raw === 'true') return true
  return raw
}

export default defineConfig(({ command }) => {
  const proxyTarget = devProxyTarget()
  const proxy = { target: proxyTarget, changeOrigin: true }
  return {
    base: command === 'build' ? '/ui/' : '/',
    plugins: [react()],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, 'src'),
      },
    },
    server: {
      host: devServerHost(),
      port: 5273,
      proxy: {
        '/api': proxy,
        '/v1': proxy,
        '/health': proxy,
        // 诊断页的 pprof 链接是绝对路径，开发期也要能打开。
        '/debug': proxy,
      },
    },
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      chunkSizeWarningLimit: 1200,
      rollupOptions: {
        output: {
          // 图表库与 React 运行时独立成 chunk：前者只随图表页按需加载，
          // 后者长期缓存不随业务代码发版失效。
          manualChunks: {
            charts: ['recharts'],
            vendor: ['react', 'react-dom', 'react-router-dom', 'swr'],
          },
        },
      },
    },
  }
})
