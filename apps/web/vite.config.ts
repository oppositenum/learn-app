import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  server: {
    proxy: {
			'/api': process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080',
      '/ws': {
				target: (process.env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080').replace(/^http/, 'ws'),
        ws: true,
      },
    },
  },
})
