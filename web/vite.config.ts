import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    // Mirrors the `paths` entry in tsconfig.json. Both are needed: TypeScript
    // resolves imports for the typechecker, Vite resolves them for the bundle, and
    // they do not read each other's configuration.
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    // In development the API runs separately, so /api is proxied to it. This
    // mirrors what nginx does in the container, which means the frontend only ever
    // talks to its own origin — no absolute URL baked in at build time, and no
    // CORS in either environment.
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
    rollupOptions: {
      output: {
        // The charting library is over half the bundle and only the dashboard's
        // flow chart needs it. Splitting it out means the login page — the first
        // thing anybody loads — does not pay for it.
        manualChunks: {
          charts: ['recharts'],
        },
      },
    },
  },
})
