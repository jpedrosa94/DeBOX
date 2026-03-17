import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    environment: 'node',
    globals: true,
    env: {
      VITE_SEAL_PACKAGE_ID: '0x1d1bc0019d623cc5d1c0e67e3f024a531197378c3ea32d34a36fb2f49541ebe9',
    },
  },
})
