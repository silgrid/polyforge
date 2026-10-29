import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The console is served under /web/ only: every built asset href is
// same-origin under that prefix (base '/web/'), so the CSP's
// script-src/style-src 'self' holds and no request can leave the origin.
export default defineConfig({
  base: '/web/',
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: false,
  },
})
