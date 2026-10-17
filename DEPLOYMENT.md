# Deployment Guide

## Requirements

- A server with Docker and Docker Compose installed
- A domain name with an **A record pointing to your server's public IP**
- Ports **80** and **443** open (Caddy uses port 80 for the Let's Encrypt HTTP challenge)

## Deploy

**1. Clone the repo on your server:**

```bash
git clone https://github.com/faisal-smarthub/Hades.git
cd Hades
```

**2. Edit `config/prod.yaml`:**

Two fields are required:

```yaml
server:
  registryHost: "yourdomain.com"   # your domain

totp:
  encryptionKey: "<64-char hex>"   # generate: openssl rand -hex 32
```

Everything else in `config/prod.yaml` is pre-configured for the Docker Compose stack (Postgres, Gitaly, Redis all use the internal Docker network; SDK artifacts go to local disk, not S3).

**3. Create the data directories and start:**

All state (Postgres, Gitaly repositories, SDK artifacts, Caddy's certs) is
bind-mounted under `./data` rather than named Docker volumes, specifically so
the whole service is one directory tree a backup tool like `restic` can point
at directly. The `hades` container runs as a non-root user (uid `10001`), so
its artifact directory needs to be writable by that uid before first start:

```bash
mkdir -p data/postgres data/gitaly data/artifacts data/caddy/data data/caddy/config
sudo chown -R 10001:10001 data/artifacts
```

Then:

```bash
DOMAIN=yourdomain.com docker compose up -d
```

That's it. Caddy automatically obtains a TLS certificate from Let's Encrypt on first start.

## What runs

| Service | Role |
|---|---|
| `caddy` | TLS termination, reverse proxy (Let's Encrypt auto-cert) |
| `hades` | Schema registry API (h2c on port 50051, internal) |
| `frontend` | Next.js web UI (port 3000, internal) |
| `postgres` | Metadata storage |
| `gitaly` | Git repository storage |
| `redis` | Cache / rate limiting |
| `migrator` | Runs DB migrations on startup, then exits |

SDK artifact storage uses the `disk` backend (`backends.artifactStorage: disk`
in `config/prod.yaml`), writing into the `hades` container's
`./data/artifacts` bind mount — no S3/MinIO dependency.

## Backups

Everything durable lives under `./data`:

```bash
restic backup ./data
```

`postgres` and `gitaly` write to their data directories continuously, so for
a consistent snapshot either accept the small window of inconsistency a live
backup gives you, or briefly stop the stack (`docker compose stop postgres
gitaly`) before backing up and `docker compose start postgres gitaly` after.

## Optional: change credentials

Default credentials are set in `docker-compose.yml` via environment variables. Override them with a `.env` file:

```bash
POSTGRES_PASSWORD=strongpassword
```

Update the matching value in `config/prod.yaml` if you change the Postgres password.

## OPA cache and multi-pod deployments

Hades caches per-subject OPA role bindings in the configured cache backend. The production Docker Compose stack uses Redis, which is correct for multi-pod setups.

> **Warning:** If you switch `backends.cache` to `memory`, each pod keeps its own in-process binding cache. Bindings revoked on one pod (e.g., `RemoveOrgMember`) will not propagate to other pods until the TTL expires (default 60s for Redis, 10s for memory). During that window, a removed member may still pass authorization checks on pods they happen to reach. **For any deployment with more than one Hades pod, `backends.cache: redis` is required for timely binding revocation.**

SQLite is single-process by design and pairs naturally with the in-memory cache; multi-pod with SQLite is not a supported configuration.

## Updates

```bash
docker compose pull
docker compose up -d
```

The `migrator` service runs automatically on startup and applies any new migrations.
