---
name: release-server-image
description: Test, build and publish a new doinspect image (arm64, scratch) to the Gitea registry of the Pi setup, and roll it out on the Docker VM. Use when the code changed or the user asks to deploy a new doinspect version.
---

# Release the doinspect image

Values come from `../raspberry-infra/.env` (`set -a; . ../raspberry-infra/.env; set +a`); secrets from Agent Vault on the Pi host. Never print a secret.

1. Checks, all must pass: `cd server && go vet ./... && go test -race ./...`.
2. Build from the repository root: `docker buildx build --platform linux/arm64 --build-arg VERSION=<x.y.z> -f server/Dockerfile -t doinspect:<x.y.z> --load .`
3. Push to Gitea's registry with the push-only token `GITEA_PACKAGE_TOKEN`, then log out. Docker on the Mac (Colima) usually cannot reach the registry, so push from the Docker VM (from a Linux machine, `podman build --format docker` and `podman save --format docker-archive` do the same): `docker save doinspect:<x.y.z> | gzip | ssh $SYSTEM_USER@$HOST_STATIC_IP "ssh ubuntu@$DOCKER_VM_IP 'gunzip | docker load'"`, then on the Docker VM `docker login gitea.$DOCKER_DOMAIN` with the token piped from the host's Agent Vault, `docker tag`, `docker push gitea.$DOCKER_DOMAIN/$GITEA_ADMIN_USER/doinspect:<x.y.z>`, `docker logout`, and `docker rmi doinspect:<x.y.z>`.
4. Roll out: set `DOINSPECT_VERSION` in `../raspberry-infra/bashScript/config.sh`, then either re-run `04` on the Pi, or set `DOINSPECT_VERSION=<x.y.z>` in `/opt/docker/.env` on the Docker VM and run `cd /opt/docker && docker compose up -d doinspect` there. A restart loses at most the last minute: the snapshot in `/data/doinspect.snap` is loaded on start.
5. Check: `https://$DOCKER_DOMAIN/health` reports the new version (Traefik routes to the new container a few seconds after its health check passes; 404 until then). For the machines and their `LastSeen`, log in first: on the Pi host, pipe `DOINSPECT_PASSWORD` from Agent Vault into `curl -c <jar> --data-urlencode password@- https://$DOCKER_DOMAIN/login`, then `curl -b <jar> https://$DOCKER_DOMAIN/health`.
