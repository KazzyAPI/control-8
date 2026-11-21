/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { readFileSync } from 'node:fs'
import { defineConfig } from 'vite'

const { version } = JSON.parse(readFileSync('./package.json', 'utf8'))

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { assetsInlineLimit: 0 }, // CSP has no data: images; every asset is a real file
  define: { __C8_VERSION__: JSON.stringify(version) },
  server: { proxy: { '/api': 'http://localhost:8000', '/auth': 'http://localhost:8000' } },
  test: { environment: 'jsdom', include: ['src/**/*.test.{ts,tsx}'] },
})
