import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [vue()],
  base: '/ui/',
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks: undefined,
      },
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8085',
        changeOrigin: true,
      },
      // Copied MCP endpoints use the page origin, so the dev server must
      // forward MCP traffic to the core as well.
      '/mcp': {
        target: 'http://localhost:8085',
        changeOrigin: true,
      },
      '/events': {
        target: 'http://localhost:8085',
        changeOrigin: true,
        ws: true,
      },
    },
  },
})
