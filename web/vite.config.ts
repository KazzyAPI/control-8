/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [react()],
  server: { proxy: { '/api': 'http://localhost:8000', '/auth': 'http://localhost:8000', '/login': 'http://localhost:8000' } },
  test: { environment: 'jsdom', include: ['src/**/*.test.{ts,tsx}'] },
})
