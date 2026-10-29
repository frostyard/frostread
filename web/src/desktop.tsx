// The desktop app's start (ADR-0008): the standalone Flatpak's window, with
// its own readd. The lodge entry is main.tsx.
import "./app.css";
import { startDesktop } from "./desktop/start.js";

const root = document.getElementById("root");
if (root === null) throw new Error("read-web: #root is missing");

await startDesktop({ root });
