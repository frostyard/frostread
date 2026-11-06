// Targeted cleanup tests for the e2e harness (no app or display). Run with:
// pnpm exec vitest run -c vitest.e2e.config.ts e2e/harness-stop.e2e.ts
import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import { cleanupRecordedProcesses, end, pidAlive, readPids } from "./harness";

const dirs: string[] = [];

afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { force: true, maxRetries: 10, recursive: true });
});

function tempDir(): string {
  const dir = mkdtempSync(join(tmpdir(), "frostread-harness-stop-"));
  dirs.push(dir);
  return dir;
}

describe("the e2e harness cleanup", () => {
  it("end SIGKILLs a child that ignores SIGTERM", async () => {
    // Revert check: without SIGKILL in end(), this process stays alive and
    // end() rejects after the short term wait.
    const proc = spawn("sh", ["-c", 'trap "" TERM; while :; do :; done'], { stdio: "ignore" });
    expect(proc.pid).toBeGreaterThan(0);

    try {
      await end("TERM-ignoring child", proc, 100, 500);
      expect(proc.pid === undefined || !pidAlive(proc.pid)).toBe(true);
    } finally {
      if (proc.pid !== undefined && pidAlive(proc.pid)) process.kill(proc.pid, "SIGKILL");
    }
  });

  it("kills a late launcher before reading readd pids", async () => {
    // Revert check: if stop() reads readd-pids before it SIGKILLs launchers,
    // a launcher can append/start another readd after that read.
    const dir = tempDir();
    const launcherFile = join(dir, "launcher-pids");
    const readdFile = join(dir, "readd-pids");

    const readd = spawn("sh", ["-c", 'trap "" TERM; while :; do :; done'], { stdio: "ignore" });
    expect(readd.pid).toBeGreaterThan(0);
    const readdPid = readd.pid as number;
    writeFileSync(readdFile, `${readdPid}\n`);

    const launcher = spawn(
      "sh",
      [
        "-c",
        `printf '%s\\n' "$$" >> '${launcherFile}'; trap "" TERM; (sleep 0.2; printf '%s\\n' '${readdPid}' >> '${readdFile}') & while :; do :; done`,
      ],
      { stdio: "ignore" },
    );

    const deadline = Date.now() + 2_000;
    while (!existsSync(launcherFile) && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    expect(readPids(launcherFile)).toContain(launcher.pid);

    try {
      const problems = await cleanupRecordedProcesses(launcherFile, readdFile, [], 100, 500);
      expect(problems).toEqual([]);
      expect(launcher.pid === undefined || !pidAlive(launcher.pid)).toBe(true);
      expect(!pidAlive(readdPid)).toBe(true);
    } finally {
      if (launcher.pid !== undefined && pidAlive(launcher.pid)) process.kill(launcher.pid, "SIGKILL");
      if (pidAlive(readdPid)) process.kill(readdPid, "SIGKILL");
      rmSync(dir, { force: true, maxRetries: 10, recursive: true });
    }
  });
});
