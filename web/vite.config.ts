/// <reference types="vitest/config" />
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The bundle lodge serves from /usr/share/lodge/apps/read/web/ at
// /app/read/ (lodge's app-contract.md, Public URLs). The Makefile's
// web-build builds it into build/web.
// Inside an nsl machine, host file edits raise no inotify events, so the
// watcher polls (as frostmail's vite.config does).
const poll = process.env.NSL === "1";

export default defineConfig({
  base: "/app/read/",
  plugins: [react(), tailwindcss()],
  server: poll ? { watch: { usePolling: true, interval: 300 } } : {},
  build: { target: "es2022", outDir: "../build/web", emptyOutDir: true },
  test: {
    environment: "happy-dom",
    // Dates read the same in UTC wherever the tests run.
    env: { TZ: "UTC" },
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["src/testing/setup.ts"],
  },
});
