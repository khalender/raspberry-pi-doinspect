# doinspect server

Go 1.27, no cgo. One process: a collector that reads every machine once per interval, a ring buffer per series, a snapshot file once a minute, a check of every service once a minute, and an HTTP server with its own login.

```bash
go test ./...
DOINSPECT_TARGETS=node=http://127.0.0.1:9100/metrics DOINSPECT_LOCAL=- DOINSPECT_DATA=./tmp-data \
  DOINSPECT_LISTEN=127.0.0.1:8080 DOINSPECT_SERVICES=- DOINSPECT_PASSWORD_HASH=- go run ./cmd/doinspect
curl -s 127.0.0.1:8080/health; curl -s 127.0.0.1:8080/api/last
```

Pages (HTML with SVG charts drawn by the server, no JavaScript, refresh every 30 s, Pastel Outline look, the name doInspect): `/` (home: a summary card that lists what needs attention, the services as tiles by group with whether they answer, a card per machine, the host's memory split, the six biggest containers, the network card), `/apps` (every service and every container; `/containers` redirects here), `/machines` (a card and CPU and memory columns per machine), `/machine/{name}` (every figure as a line chart), `/network` (when network checks are set up), each with `?range=1h|6h|24h|7d` (default 24h). Column charts show the peak of each part of the range; a line chart draws at most 300 steps, the line each step's average and the light band its peak. A machine whose last report is more than about two intervals old shows as missing. A container that went quiet counts on the summary card after 2 minutes, for an hour, or for as long as a service names it; a gone series under a bare container id is the same container before `containers.txt` named it and is not shown. Nunito Sans ships with the binary (`internal/web/static`, SIL Open Font License), so the pages load nothing from outside.

Login: without a session every page shows a password form and the service links, nothing else; `/api/*` answers 401. One password, checked against `DOINSPECT_PASSWORD_HASH` (bcrypt); the session is a signed cookie (`HttpOnly`, `Secure` behind HTTPS, `SameSite=Strict`) valid 30 days, signed with a key in `DOINSPECT_DATA/session.key` that doinspect creates on first start. 5 wrong passwords from one address make it wait a minute; the address is the last entry of `X-Forwarded-For`, which Traefik appends. `/health` and `/static/` stay open; `/health` gives only status and version without a session. `echo 'the password' | doinspect hash` prints the hash.

Services: `services.yml` is a list of entries with `name`, `url`, `description`, `group`, optional `note`, `container` and `icon` (also accepted: a `services:` list, or groups that hold one). doinspect reads it again when the file changes and keeps the last good list when it does not parse. Once a minute it requests every URL whose name resolves to a LAN address (private ranges, loopback, link-local, 100.64.0.0/10) and never dials anything else: a status below 500 is up (401 included, a redirect is not followed), a certificate the container does not trust still counts as up with a note, and a failure is tried once more after 2 s. Series `svc/http[<name>].up` (0 or 1) and `.ms`.

Icons: the service links are tiles with a pixel-art icon (`internal/web/icons.go`): 16 rows of 16 characters, one per cell, `.` empty and a letter for a colour of `iconPalette`, drawn as SVG squares with crisp edges. Keys: `adblock`, `router`, `firewall`, `git`, `containers`, `vault`, `cloud-files`, `phone-sync`, `notebook`, `bird`, `globe`, `database`, `photos`, `chart`, `lock`, `box` (the fallback), and `inspect` (the magnifying glass of the logo and `static/icon.svg`). `icon:` in `services.yml` picks one; without it the service and container names are matched against `iconAliases`. To add an icon, add a grid and, if it fits, an alias; `TestIcons` checks every grid. After changing `inspect`, run `UPDATE_ICON=1 go test ./internal/web -run TestIconFile` to rewrite `static/icon.svg`. The stylesheet and the icon are linked with `?v=<version>`, because static files are cached for a day.

Reading the local machine needs Linux: run it in a container with `-v /proc:/host/proc:ro -v /sys:/host/sys:ro`.

| Variable | Default | Meaning |
|----------|---------|---------|
| `DOINSPECT_TARGETS` | none | node_exporter pages to scrape: `name=url` pairs, comma separated |
| `DOINSPECT_LOCAL` | `docker-vm` | name of the machine doinspect runs on; `-` reads no local figures |
| `DOINSPECT_PROC`, `DOINSPECT_SYS` | `/host/proc`, `/host/sys` | that machine's `/proc` and `/sys`, mounted read-only |
| `DOINSPECT_FS` | `/data` | paths whose file system to report for that machine, comma separated |
| `DOINSPECT_NAMES` | `/data/containers.txt` | container ids and names, one `<id> <name>` per line, written every minute by `docker ps --no-trunc --format '{{.ID}} {{.Names}}'` outside doinspect |
| `DOINSPECT_INTERVAL` | `15s` | one point per series per interval, whole seconds |
| `DOINSPECT_KEEP` | `168h` | how long points are kept |
| `DOINSPECT_TIMEOUT` | `5s` | per scrape |
| `DOINSPECT_DATA` | `/data` | folder for `doinspect.snap` |
| `DOINSPECT_LISTEN` | `:8080` | listen address |
| `DOINSPECT_PING` | none | ping targets, `name=address` pairs, comma separated; 10 echo requests 100 ms apart per target each interval |
| `DOINSPECT_DNS` | none | DNS servers to time, `name=host:port` pairs; one lookup (A and AAAA) each interval |
| `DOINSPECT_DNS_NAME` | `cloudflare.com` | the name looked up |
| `DOINSPECT_SPEEDTEST_EVERY` | `6h` | gap between speed tests, 1m to 24h; `0` runs none |
| `DOINSPECT_SPEEDTEST_AT` | `03:00` | local time of the first test of the day (`TZ`); with 6h: 03:00, 09:00, 15:00, 21:00 |
| `DOINSPECT_SPEEDTEST_BYTES` | `100000000` | bytes each way, in requests of at most 25 MB, 4 at a time |
| `DOINSPECT_SPEEDTEST_PING` | `internet` | ping target timed before (idle) and during (loaded) the transfers |
| `DOINSPECT_SPEEDTEST_CPU` | `pi-host` | machine whose CPU peak the network page shows next to each test |
| `DOINSPECT_SPEEDTEST_URL` | `https://speed.cloudflare.com` | speed test (`__down`, `__up`, `/cdn-cgi/trace`) |
| `DOINSPECT_ISP_URL` | RIPEstat `prefix-overview` | AS number and holder of the public address |
| `DOINSPECT_SERVICES` | `/services/current/services.yml` | the service list; `-` shows none |
| `DOINSPECT_SERVICE_EVERY` | `1m` | gap between service checks, at least 10s; `0` checks none |
| `DOINSPECT_ARCANE` | none | link on the summary card; empty takes the service named Arcane |
| `DOINSPECT_HOST` | `pi-host` | the machine the VMs run on, for the memory card |
| `DOINSPECT_PASSWORD_HASH` | none, required | bcrypt hash of the login password; `-` serves every page without a login |
| `TZ` | `UTC` | time zone of the speed test times; the zone data is built into the binary |

Series are named `<machine>/<figure>`: `cpu` and `cpu[N]` (percent busy), `ram.used`, `ram.total`, `ram.cached`, `swap.used` (bytes), `load1`, `load5`, `load15`, `disk[dev].read` and `.write`, `net[if].rx` and `.tx` (bytes per second), `fs[path].used` (percent) and `.size` (bytes), `uptime` (seconds), `temp` (°C), `freq` (MHz); per container on the local machine `ctr[name].ram` (bytes, without inactive file cache, as `docker stats`), `ctr[name].cpu` (percent of the whole machine) and `ctr[name].rx`, `.tx` (bytes per second).

Network series: `net/ping[name].rtt` and `.jitter` (ms), `.loss` (percent), `net/dns[name].ms` (ms) and `.fail` (0 or 1). Pings use the kernel's unprivileged ICMP socket, which needs no capability, only the container's group in `net.ipv4.ping_group_range` (Docker sets `0 2147483647` for a container with its own network). Speed tests are kept 30 days in `speedtest.json` next to the snapshot, written after each test; `GET /api/speedtest` lists them. Cloudflare's `/meta`, which named the ISP, answers `{}` to programs, so the ISP comes from RIPEstat.

Containers are found in the cgroup v2 folders under `/sys/fs/cgroup` (systemd or cgroupfs driver). Traffic comes from `/proc/<pid>/net/dev` of one of the container's processes, found through `/proc/<pid>/cgroup`; `cgroup.procs` cannot be used, because inside a container it lists every outside process as pid 0.

Packages: `internal/raw` (one reading, from a node_exporter page or from `/proc`), `internal/derive` (readings to samples and rates), `internal/ring` (ring buffers and the snapshot), `internal/collect` (the loop), `internal/web` (pages and charts), `internal/netcheck` (ping, DNS, speed test), `internal/services` (the service list and its checks), `internal/auth` (login), `internal/config` (`DOINSPECT_*`).
