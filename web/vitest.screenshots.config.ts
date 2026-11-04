// The Flatpak metainfo screenshots (make screenshots). This suite is not
// in vitest.e2e.config.ts, so make app-e2e does not run it.
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "node",
    include: ["e2e/screenshots.suite.ts"],
    testTimeout: 300_000,
    hookTimeout: 600_000,
    fileParallelism: false,
  },
});
