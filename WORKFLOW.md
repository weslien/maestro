---
tracker:
  kind: github_project
  owner: "weslien"
  project_number: 3
  repo: "weslien/maestro"
workspace:
  root: .maestro/worktrees
agent:
  max_concurrent: 5
  model: sonnet
  research_model: opus
  planning_model: opus
  execution_model: sonnet
  validation_model: sonnet
  max_budget_per_issue: 10.00
  permission_mode: plan
polling:
  interval: 30s
tmux:
  session_prefix: maestro
bridge:
  webhook_url: "https://maestro.metal.fnz-qhub.com/webhook"
---
You are working on issue #{{ issue.number }}: {{ issue.title }}

## Phase: {{ phase }}

## Issue Description
{{ issue.body }}

## Instructions
Follow the GSD methodology for the {{ phase }} phase. Be thorough, write tests, and make atomic commits.
