// App tests: the built app in WebKitGTK through WebKitWebDriver (make
// app-e2e, plan 0002, step 3.6).
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "node",
    include: ["e2e/**/*.e2e.ts"],
    testTimeout: 300_000,
    // A launch's own worst case is 525 s (web/e2e/harness.ts, launch);
    // the hook keeps room above it.
    hookTimeout: 600_000,
    fileParallelism: false,
  },
});
