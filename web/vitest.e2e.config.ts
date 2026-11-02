// App tests: the built app in WebKitGTK through WebKitWebDriver (make
// app-e2e, plan 0002, step 3.6).
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "node",
    include: ["e2e/**/*.e2e.ts"],
    testTimeout: 300_000,
    hookTimeout: 300_000,
    fileParallelism: false,
  },
});
