// The bundle lodge serves from /usr/share/lodge/apps/read/web/ at
// /app/read/ (lodge's app-contract.md, Public URLs).
import { defineConfig } from "vite";

export default defineConfig({
  base: "/app/read/",
  build: { target: "es2022" },
});
