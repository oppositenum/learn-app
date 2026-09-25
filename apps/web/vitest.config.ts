import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [vue()],
  test: {
    // Vitest blanks every CSS import by default, including ?raw; keep raw CSS text for rule assertions.
    css: { include: [/\.css\?raw$/] },
    environment: 'jsdom',
    globals: true,
  },
})
