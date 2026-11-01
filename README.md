# doinspect

Load of a small home server setup: CPU, RAM, load, disks, network and temperature of each machine and container, one point every 15 seconds, kept for 7 days, in about 20 MB of RAM.

- `server/`: the Go program. It scrapes [node_exporter](https://github.com/prometheus/node_exporter) pages, reads the local machine's `/proc` and `/sys`, and keeps every series in a ring buffer with a snapshot file. See [server/README.md](server/README.md).

Licence: MIT.
