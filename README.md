# Maestro

GSD-powered GitHub Projects orchestrator that uses Claude Code agents to autonomously work through issues.

Maestro polls a GitHub Projects V2 board, picks up issues, and runs each through a multi-phase lifecycle — Research, Planning, Implementation, and Validation — spawning Claude Code in isolated git worktrees with full context continuity across phases.

## Install

```bash
go install github.com/weslien/maestro/cmd/maestro@latest
```

Or build from source:

```bash
git clone https://github.com/weslien/maestro.git
cd maestro
go build -o ~/bin/maestro ./cmd/maestro/
```

## Prerequisites

- [Go 1.21+](https://go.dev/dl/)
- [GitHub CLI](https://cli.github.com/) (`gh`) — authenticated via `gh auth login`
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) (`claude`) — installed and authenticated
- [tmux](https://github.com/tmux/tmux)

## Quick Start

```bash
cd your-project        # any git repo with a GitHub remote
maestro init           # auto-detects owner/repo, creates WORKFLOW.md, updates .gitignore
```

Set `tracker.project_number` in `WORKFLOW.md` to your GitHub Project board number, or create a new one:

```bash
maestro setup          # creates a GitHub Project with all required status columns
```

Then run:

```bash
maestro run            # TUI dashboard
maestro run --headless # structured JSON log output
```

## How It Works

Maestro watches your GitHub Project board and processes issues through a GSD lifecycle:

```
Todo → Research → Planning → In Progress → Validation → Human Review → Done
                                  ↑              |
                                  └── (retry ≤2) ┘
```

Each phase spawns Claude Code with a phase-specific prompt. Session IDs persist across phases so Claude retains full context from research through validation.

**Per-issue artifacts** are stored in `.maestro/` inside each worktree:

| File | Phase | Contents |
|------|-------|----------|
| `research.md` | Research | Codebase exploration, problem analysis |
| `plan.md` | Planning | Tasks, acceptance criteria, verification steps |
| `progress.md` | In Progress | Execution log, commits made |
| `validation.md` | Validation | Test results, acceptance criteria check |

### Workspace Isolation

Each issue gets its own git worktree at `.maestro/worktrees/issue-<number>/` on a branch `maestro/issue-<number>`. This means agents work on isolated branches without interfering with each other or your working copy.

## GitHub Project Setup

Your project board needs these status columns:

`Backlog` · `Todo` · `Research` · `Planning` · `In Progress` · `Validation` · `Human Review` · `Done` · `Cancelled`

Run `maestro setup` to create a project with these automatically.

## Configuration

`maestro init` generates a `WORKFLOW.md` with settings auto-detected from your git remotes. The file has two parts: YAML frontmatter for config, and a prompt template body.

```yaml
---
tracker:
  kind: github_project
  owner: "weslien"              # auto-detected from git remote
  project_number: 3             # your GitHub Project number
  repo: "weslien/my-project"    # auto-detected from git remote
workspace:
  root: .maestro/worktrees      # git worktrees, auto-gitignored
agent:
  max_concurrent: 5             # parallel issue limit
  model: sonnet                 # default model
  research_model: opus          # use opus for deeper research
  planning_model: opus          # use opus for planning
  execution_model: sonnet       # sonnet for implementation
  validation_model: sonnet      # sonnet for validation
  max_budget_per_issue: 10.00   # USD budget cap per issue
  permission_mode: plan         # claude permission mode
polling:
  interval: 30s
tmux:
  session_prefix: maestro
---
You are working on issue #{{ issue.number }}: {{ issue.title }}

## Phase: {{ phase }}

## Issue Description
{{ issue.body }}

## Instructions
Follow the GSD methodology for the {{ phase }} phase.
```

### Auto-Detection

When no `WORKFLOW.md` exists, maestro auto-detects your repo from git remotes (tries `maestro` remote first, then `origin`) and uses sensible defaults. You only need the config file to set `project_number` or customize models/prompts.

### Prompt Templates

The prompt template uses [Pongo2](https://github.com/flosch/pongo2) (Jinja2-compatible) syntax. Available variables:

| Variable | Description |
|----------|-------------|
| `{{ issue.number }}` | Issue number |
| `{{ issue.title }}` | Issue title |
| `{{ issue.body }}` | Issue body/description |
| `{{ issue.labels }}` | Issue labels |
| `{{ phase }}` | Current phase name |

## Commands

| Command | Description |
|---------|-------------|
| `maestro init` | Auto-detect repo and create `WORKFLOW.md` |
| `maestro setup` | Create a GitHub Project with required status columns |
| `maestro run` | Start daemon with TUI dashboard |
| `maestro run --headless` | Start daemon with JSON log output |
| `maestro status` | One-shot table of project issues |
| `maestro stop <number>` | Stop the agent working on an issue |

## TUI Controls

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate issues |
| `Enter` | Attach to issue's tmux session |
| `s` | Stop selected issue's agent |
| `r` | Refresh issue list |
| `q` | Quit |

## Architecture

```
WORKFLOW.md (config + prompts)
        |
    [Config] ──────────────────────────────┐
        |                                  |
    [Tracker]         [Workspace]     [Agent Runner]
  (gh api graphql)   (git worktree)   (claude CLI + tmux)
        |                 |                |
        └────── [Orchestrator] ────────────┘
                  (poll loop)
                      |
                 [Events Channel]
                      |
                    [TUI]
              (bubbletea dashboard)
```

## License

MIT
