---
name: work-on-specs
description: How the specs of doinspect are laid out and how to work with them. Use cases, tasks, spikes, decisions, questions and bug records live in the private specs repository next to this one (folder doinspect-app) and are linked by ID. Use when the user asks for a feature, a change, a fix, a spike or upkeep, names a task, maintenance item or use case ("do T05", "do M02"), asks an open question, asks "what is left", or before writing any code for a task.
---

# Work with the specs

The specs of doinspect are in `../raspberry-pi-apps-functional-specs/doinspect-app/`. Read `README.md` there first, then that repository's `CLAUDE.md`.

## The chain

```
spike (SP)  ->  use case (UC)  <-  task (T)  ->  release
                     ^
            decision (D), question (Q), bug (B)
```

| Kind | File | Holds |
|---|---|---|
| Use case | `spec/<GROUP>/UC-<GROUP>-<NNN>-<name>.md` | The spec. `## Behaviour` says what must hold, numbered. `## Build` says how it is built. `## Open questions` lists its questions. The header names its requirements (`F…`), `Verified by`, `Built by` (tasks) and `Bugs`. |
| Task | `tasks/T<NNN>-<name>.md`, index `tasks.md` | The work. Header: `Type`, `Status`, `Implements` (use cases), `Depends on`, and `Fixes` for a fix. Then what was asked, what was built, the checks. |
| Spike | `spikes/SP<NNN>-<name>.md` | Work before a spec: the question, findings with sources, a prototype, the user's answers. Header: `Status`, `Task`, `Produced`. |
| Question | `questions.md` | `## Q<NNN> — title`, then `Status · Asked · Affects: use cases`, the question, and `Answer:`. |
| Bug | `bugs/B<NNN>-<name>.md`, index `bugs.md` | What was reported, the cause, what did not work, the fix, how to spot it again, the guard. Kept for good. Skill: `track-bugs`. |
| Decision | `decisions.md` | `## D<n>. title (date)`, who decided, what, why, the cost. Never rewritten; a new one replaces it. |

## Before writing code for a task

1. Read the task file, then every use case on its `Implements` line, whole.
2. Read the questions those use cases list. An open question that the work depends on is asked first, not guessed.
3. Build what the use case says. Where it has pictures or a prototype beside it, they are the reference.
4. When the task and the use case disagree, the use case wins. Say so and fix the task.

## A new request

- Something unclear, with choices to make: a **spike**. New file in `spikes/`, a row in `spikes/README.md`, a task that points to it. A spike builds nothing. Prototypes go in `tempwork/` of this repository; what the spec needs to keep is copied beside the use case.
- Clear behaviour: write or change the **use case** first, then add the **task** with that use case on `Implements`.
- Something reported as broken: follow the skill `track-bugs` first. It ends in a bug record, and for a real bug in a task of type `fix` with `Fixes: B…`.
- Work on the specs or the tooling: a task of type `chore`; its `Implements` may say `none (not product behaviour)`.
- A task never carries requirements of its own. If you are writing "must" in a task, it belongs in the use case.

## Questions

Write a question into `questions.md` at the moment you ask it: next free `Q` number, `Status: Open`, when and where it came up, the use cases it affects, and add it to those use cases' `## Open questions`. Never leave a question only in a chat answer or in a task's text. When the user answers: write the decision (`decisions.md`) or change the use case, quote the user's words, remove the question from the register and from the use cases.

## Verification

A use case is checked by the server's tests and on the running dashboard. Write on `Verified by` what covers it. Say what was not checked.

## When a task is done

1. Run its checks. Report failures as failures.
2. Update the use case: `Status`, the `## Build` section to what now runs, `Verified by`, `Built by`. Update `design/architecture.md` when the live system changed.
3. Record the result in the task file with the date: what was built, where, the checks with their numbers, what was not checked.
4. Set the status in the task file and in `tasks.md`. They must agree.

## After every change to the specs

```bash
cd ../raspberry-pi-apps-functional-specs
python3 tools/speclint.py doinspect-app    # 0 errors before you stop; say which warnings remain
python3 build-index.py
```

## Rules

- Execute a task or a maintenance item only when the user names it. When the user asks "how", explain and wait.
- IDs: the ID comes first in a file name; text cites the ID (`T05`, `UC-…`, `D4`, `Q001`, `B001`). An ID is never changed or used again. From another project, write the project in front (`doinspect T05`).
- The security measures in `decisions.md` (D3, D5) are requirements: no root, no Docker socket, metrics ports open to the Docker VM only.
- Deploying is an infra task (`../raspberry-pi-apps-functional-specs/infra/tasks.md`, T23), done with the `../raspberry-infra` skills, including the dashboard entry and the secrets backup reminder.
- Never write real values (domain, addresses, tokens) into either repository.
- Commit and push only when the user asks. Several sessions may edit the specs: change your own files and lines, and say what you left uncommitted.
