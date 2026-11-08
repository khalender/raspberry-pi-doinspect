# doinspect server

Go 1.27, no cgo. One process: a collector that reads every machine once per interval, a ring buffer per series, a snapshot file once a minute, and an HTTP server.

```bash
go test ./...
DOINSPECT_TARGETS=node=http://127.0.0.1:9100/metrics DOINSPECT_LOCAL=- DOINSPECT_DATA=./tmp-data \
  DOINSPECT_LISTEN=127.0.0.1:8080 go run ./cmd/doinspect
curl -s 127.0.0.1:8080/health; curl -s 127.0.0.1:8080/api/last
```

Pages (HTML with SVG charts drawn by the server, no JavaScript, refresh every 30 s): `/` (machines, the network card and the biggest containers), `/containers`, `/machine/{name}`, `/network` (when network checks are set up), each with `?range=1h|6h|24h|7d` (default 24h). A chart draws at most 300 steps: the line is each step's average, the light band its peak. A machine whose last report is more than about two intervals old shows as missing. JetBrains Mono ships with the binary (`internal/web/static`, SIL Open Font License), so the pages load nothing from outside.

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
| `TZ` | `UTC` | time zone of the speed test times; the zone data is built into the binary |

Series are named `<machine>/<figure>`: `cpu` and `cpu[N]` (percent busy), `ram.used`, `ram.total`, `ram.cached`, `swap.used` (bytes), `load1`, `load5`, `load15`, `disk[dev].read` and `.write`, `net[if].rx` and `.tx` (bytes per second), `fs[path].used` (percent) and `.size` (bytes), `uptime` (seconds), `temp` (°C), `freq` (MHz); per container on the local machine `ctr[name].ram` (bytes, without inactive file cache, as `docker stats`), `ctr[name].cpu` (percent of the whole machine) and `ctr[name].rx`, `.tx` (bytes per second).

Network series: `net/ping[name].rtt` and `.jitter` (ms), `.loss` (percent), `net/dns[name].ms` (ms) and `.fail` (0 or 1). Pings use the kernel's unprivileged ICMP socket, which needs no capability, only the container's group in `net.ipv4.ping_group_range` (Docker sets `0 2147483647` for a container with its own network). Speed tests are kept 30 days in `speedtest.json` next to the snapshot, written after each test; `GET /api/speedtest` lists them. Cloudflare's `/meta`, which named the ISP, answers `{}` to programs, so the ISP comes from RIPEstat.

Containers are found in the cgroup v2 folders under `/sys/fs/cgroup` (systemd or cgroupfs driver). Traffic comes from `/proc/<pid>/net/dev` of one of the container's processes, found through `/proc/<pid>/cgroup`; `cgroup.procs` cannot be used, because inside a container it lists every outside process as pid 0.

Packages: `internal/raw` (one reading, from a node_exporter page or from `/proc`), `internal/derive` (readings to samples and rates), `internal/ring` (ring buffers and the snapshot), `internal/collect` (the loop), `internal/web` (pages and charts), `internal/netcheck` (ping, DNS, speed test), `internal/config` (`DOINSPECT_*`).
