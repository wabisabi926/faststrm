/// <reference types="vitest/config" />
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import path from "path";

// Vitest config: shares React plugin and @ alias with vite.config.ts, jsdom env.
// See v1.1.1 task list T1.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  test: {
    // jsdom provides DOM API for Testing Library rendering
    environment: "jsdom",
    // Global API (describe/it/expect) for Jest-like ergonomics
    globals: true,
    // setupFiles runs before each test: injects jest-dom matchers and global mocks
    setupFiles: ["./src/test/setup.ts"],
    // Only match *.test.{ts,tsx} and __tests__ dirs to avoid running source files
    include: ["src/**/*.{test,spec}.{ts,tsx}", "src/**/__tests__/**/*.{ts,tsx}"],
    // Exclude build artifacts and node_modules
    exclude: ["node_modules", "dist", "../internal/web/spa"],
    // Coverage (no threshold enforced yet; revisit after T6-T8)
    coverage: {
      provider: "v8",
      reporter: ["text", "html"],
      reportsDirectory: "./coverage",
    },
  },
});
