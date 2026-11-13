# doinspect

The home page of a small home server setup: every service with a link and whether it answers, and the load of each machine and container (CPU, RAM, load, disks, network and temperature), one point every 15 seconds, kept for 7 days, in about 60 MB of RAM. Without a login the page shows only the service links and a password form.

- `server/`: the Go program. It scrapes [node_exporter](https://github.com/prometheus/node_exporter) pages, reads the local machine's `/proc` and `/sys`, keeps every series in a ring buffer with a snapshot file, reads the service list from a `services.yml` file and checks each service once a minute. See [server/README.md](server/README.md).

Licence: MIT.
