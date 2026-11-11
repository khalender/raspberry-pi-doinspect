# doinspect: agent instructions

Shows the load of the Raspberry Pi setup: the Pi host, the OPNsense VM, the Docker VM and each container, one point every 15 s, kept 7 days. A Go server (`server/`) scrapes node_exporter on the Pi host and OPNsense, reads the Docker VM's `/proc` and `/sys` through read-only mounts, keeps every series in a ring buffer in memory with a snapshot file, and (from T03) draws the pages as HTML and SVG.

- Specs: the private repository `../raspberry-pi-apps-functional-specs/doinspect-app/`. Read its `README.md` first: use cases in `spec/`, one task per file in `tasks/`, spikes in `spikes/`, and `questions.md`, `decisions.md`, `bugs.md`. A task names its use cases on `Implements`; a question goes into `questions.md` when it is asked. Skill: `work-on-specs`.
- Specs first, never straight to code (the user, 2026-11-11). A report or a request is written into the specs before anything else: the bug record when something is broken, the use case change, and the task. Then stop and name the task. Build it only when the user names it ("do T97"). This holds also when the fix looks small or the user describes the fix.
- Something reported as broken, missing or wrong: skill `track-bugs`. Search `bugs.md` in the specs for a bug like it before looking at the code, and write the cause and the fix into its record when done.
- Throwaway work (prototypes, pictures, backups) goes in `tempwork/` here, which git ignores. Not in a shared temp folder.
- The server it runs on: `../raspberry-infra` and its skills; deployment is infra T23 and follows its `add-docker-service` skill, including the dashboard entry.

Rules: when the user asks "how", explain and wait; run a task only when the user names it. Commit and push only when asked. Never commit real values (domain, addresses, tokens). doinspect never talks to Docker and never runs as root (D3, D5).
