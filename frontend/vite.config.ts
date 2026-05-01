import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react-swc'
import path from 'path'

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
    dedupe: ['react', 'react-dom', 'react/jsx-runtime', 'react/jsx-dev-runtime', '@tanstack/react-query', '@tanstack/query-core'],
  },
  server: {
    ...(process.env.VITE_BACKEND_URL
      ? {
          proxy: {
            '/api': {
              target: process.env.VITE_BACKEND_URL,
              changeOrigin: true,
            },
          },
        }
      : {}),
  },
})
