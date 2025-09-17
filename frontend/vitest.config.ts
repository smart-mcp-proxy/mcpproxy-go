import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  test: {
    globals: true,
    environment: 'jsdom',
    coverage: {
      provider: 'v8',
      reporter: ['text', 'json', 'html'],
      exclude: [
        'node_modules/',
        'dist/',
        '**/*.d.ts',
        'coverage/**',
        'vitest.config.ts',
        'vite.config.ts'
      ]
    }
  },
  resolve: {
    alias: {
      '@': '/src'
    }
  }
})