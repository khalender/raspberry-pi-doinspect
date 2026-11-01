# doinspect server

Go 1.27, no cgo. One process: a collector that reads every machine once per interval, a ring buffer per series, a snapshot file once a minute, and an HTTP server.

```bash
go test ./...
DOINSPECT_TARGETS=node=http://127.0.0.1:9100/metrics DOINSPECT_LOCAL=- DOINSPECT_DATA=./tmp-data \
  DOINSPECT_LISTEN=127.0.0.1:8080 go run ./cmd/doinspect
curl -s 127.0.0.1:8080/health; curl -s 127.0.0.1:8080/api/last
```

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

Series are named `<machine>/<figure>`: `cpu` and `cpu[N]` (percent busy), `ram.used`, `ram.total`, `ram.cached`, `swap.used` (bytes), `load1`, `load5`, `load15`, `disk[dev].read` and `.write`, `net[if].rx` and `.tx` (bytes per second), `fs[path].used` (percent) and `.size` (bytes), `uptime` (seconds), `temp` (°C), `freq` (MHz); per container on the local machine `ctr[name].ram` (bytes, without inactive file cache, as `docker stats`), `ctr[name].cpu` (percent of the whole machine) and `ctr[name].rx`, `.tx` (bytes per second).

Containers are found in the cgroup v2 folders under `/sys/fs/cgroup` (systemd or cgroupfs driver). Traffic comes from `/proc/<pid>/net/dev` of one of the container's processes, found through `/proc/<pid>/cgroup`; `cgroup.procs` cannot be used, because inside a container it lists every outside process as pid 0.

Packages: `internal/raw` (one reading, from a node_exporter page or from `/proc`), `internal/derive` (readings to samples and rates), `internal/ring` (ring buffers and the snapshot), `internal/collect` (the loop), `internal/config` (`DOINSPECT_*`).
