---
name: work-on-specs
description: Where plans, specs, decisions and tasks for doinspect live (the private specs repository next to this one, folder doinspect-app) and how to work with them. Use when the user asks for a feature, a change, a task or maintenance item by ID ("do T02", "do M01"), an open question, or "what is left".
---

# Work with the specs

The functional and technical specs, decisions, tasks and open questions of doinspect live in the private repository `../raspberry-pi-apps-functional-specs/doinspect-app/`. Read that repository's `CLAUDE.md` first.

- A new request becomes a task there (development: `tasks.md`) or a maintenance item (work on the running service and its data: `maintenance.md`). Execute either only when the user names it; when the user asks "how", explain and wait.
- Deploying is an infra task (`../raspberry-pi-apps-functional-specs/infra/tasks.md`, T23), done with the `../raspberry-infra` skills, including the dashboard entry and the secrets backup reminder.
- The security measures in `decisions.md` (D3, D5) are requirements: no root, no Docker socket, metrics ports open to the Docker VM only.
- Never write real values (domain, addresses, tokens) into either repository.
