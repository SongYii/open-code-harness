import { defineConfig } from "vitest/config";

// No UI framework: this is a single-session, single-viewer page, not a
// general application shell, matching this project's own minimalism
// discipline (design doc §2's non-goal on frameworks/build tooling
// beyond what's needed).
export default defineConfig({
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    environment: "jsdom",
  },
});
