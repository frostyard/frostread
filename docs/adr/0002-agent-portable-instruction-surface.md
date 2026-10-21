# 0002 — Agent-portable instruction surface

- **Status:** Proposed
- **Date:** 2026-10-21

## Context

The repository is maintained by whichever coding agent is available. Each
tool reads its own instruction file (`AGENTS.md`, `CLAUDE.md`,
`.github/copilot-instructions.md`, `GEMINI.md`) and its own skills location
(`.claude/skills/`). Copies per tool drift, and drift means different
agents follow different rules.

## Decision

One canonical surface, with the other tool paths as symlinks:

- **`AGENTS.md`** holds every convention. `CLAUDE.md`, `GEMINI.md` and
  `.github/copilot-instructions.md` are symlinks to it.
- **`.agents/skills/`** is the real skills directory (`<skill>/SKILL.md`
  with YAML frontmatter). `.claude/skills` is a symlink to it.
- Content is tool-agnostic: plain markdown, no tool-specific directives.

Lodge and Porter use the same surface, so an agent that knows one repository
knows the others.

## Consequences

- Conventions are edited in one place.
- Symlinks are committed to git. GitHub's web renderer may show a link's
  target instead of its content; a native Windows checkout needs
  `core.symlinks=true` or WSL.

## Alternatives considered

- **Per-tool copies kept in sync by convention:** guaranteed drift.
  Rejected.
- **Instructions for one tool only:** wastes every other agent. Rejected.

## References

- Builds on: [ADR-0001](0001-record-architecture-decisions.md)
- Shapes: `AGENTS.md`, `.agents/skills/`, [docs/README.md](../README.md)
