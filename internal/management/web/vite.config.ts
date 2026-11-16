import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode, command }) => {
  if (command === 'build' && mode === 'mock') throw new Error('mock 仅用于本地预览和测试，不允许构建为生产产物')
  return {
    plugins: [vue()],
    base: './',
    define: { __MOCK_API__: JSON.stringify(mode === 'mock' || mode === 'test') },
    server: { host: '127.0.0.1', port: 5173, strictPort: true },
    preview: { host: '127.0.0.1', port: 4173, strictPort: true },
    build: { outDir: 'dist', emptyOutDir: true, sourcemap: false, manifest: true },
    test: { include: ['tests/**/*.test.ts'], environment: 'node' },
  }
})
