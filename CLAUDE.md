# doinspect: agent instructions

Shows the load of the Raspberry Pi setup: the Pi host, the OPNsense VM, the Docker VM and each container, one point every 15 s, kept 7 days. A Go server (`server/`) scrapes node_exporter on the Pi host and OPNsense, reads the Docker VM's `/proc` and `/sys` through read-only mounts, keeps every series in a ring buffer in memory with a snapshot file, and (from T03) draws the pages as HTML and SVG.

- Specs, decisions, tasks and open questions: the private repository `../raspberry-pi-apps-functional-specs/doinspect-app/` (read its `CLAUDE.md` first). Skill: `work-on-specs`.
- The server it runs on: `../raspberry-infra` and its skills; deployment is infra T23 and follows its `add-docker-service` skill, including the dashboard entry.

Rules: when the user asks "how", explain and wait; run a task only when the user names it. Commit and push only when asked. Never commit real values (domain, addresses, tokens). doinspect never talks to Docker and never runs as root (D3, D5).
