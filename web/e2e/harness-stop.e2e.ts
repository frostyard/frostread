// Targeted tests of the e2e harness's cleanup (no app and no display), run
// by make app-e2e with the suites.
import { type ChildProcess, spawn } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import { end, endGroup, pidAlive, until } from "./harness";

const dirs: string[] = [];
const left: number[] = [];

afterEach(() => {
  for (const pid of left.splice(0)) {
    try {
      process.kill(pid, "SIGKILL");
    } catch {
      // Already gone.
    }
  }
  for (const dir of dirs.splice(0)) rmSync(dir, { force: true, maxRetries: 10, recursive: true });
});

function tempDir(): string {
  const dir = mkdtempSync(join(tmpdir(), "frostread-harness-stop-"));
  dirs.push(dir);
  return dir;
}

/** spawnReady starts sh -c script, and waits until the script has written ready. */
async function spawnReady(script: string, ready: string, detached = false): Promise<ChildProcess> {
  const proc = spawn("sh", ["-c", script], { stdio: "ignore", detached });
  if (proc.pid !== undefined) left.push(proc.pid);
  await until("the child to say it is ready", () => existsSync(ready), 5_000);
  return proc;
}

describe("the e2e harness cleanup", () => {
  it("end kills a child that ignores SIGTERM", async () => {
    const dir = tempDir();
    const ready = join(dir, "ready");
    // The child writes ready only after its trap is set, so SIGTERM cannot
    // arrive first and end it the ordinary way.
    const proc = await spawnReady(`trap "" TERM; : > '${ready}'; while :; do sleep 1; done`, ready);
    await end("a child that ignores SIGTERM", proc, 200, 2_000);
    expect(proc.pid !== undefined && pidAlive(proc.pid)).toBe(false);
  });

  it("endGroup ends a process that the group started and nobody recorded", async () => {
    const dir = tempDir();
    const ready = join(dir, "ready");
    const grandchild = join(dir, "grandchild");
    // The leader stands for the driver, and its child for a readd that a
    // stand-in launcher started just before the cleanup, with no record
    // anywhere. Both ignore SIGTERM. The grandchild's pid goes to a file for
    // this test's own check only: endGroup never reads it.
    const leader = await spawnReady(
      `trap "" TERM; sh -c 'trap "" TERM; echo $$ > "${grandchild}"; while :; do sleep 1; done' & ` +
        `until [ -s '${grandchild}' ]; do sleep 0.05; done; : > '${ready}'; while :; do sleep 1; done`,
      ready,
      true,
    );
    const child = Number(readFileSync(grandchild, "utf8").trim());
    left.push(child);
    expect(pidAlive(child)).toBe(true);

    const problems = await endGroup(leader.pid as number, 200, 2_000);
    expect(problems).toEqual([]);
    expect(pidAlive(child)).toBe(false);
    expect(pidAlive(leader.pid as number)).toBe(false);
  });
});
