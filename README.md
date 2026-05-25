# StackFly

A lightweight, self-hosted PaaS for a single server. Deploy apps with `git push`, manage everything from a clean web UI. Heroku-like workflow without the platform tax.

## Features

- **Git push to deploy** via Tailscale SSH
- **Automatic builds** with Nixpacks (Node, Python, Go, Ruby, PHP, Java, and more)
- **Procfile support** — web, worker, release processes
- **Process scaling** — run multiple replicas with load balancing
- **Custom domains** with optional automatic HTTPS (Let's Encrypt via Caddy)
- **Add-ons** — managed PostgreSQL, MySQL, Redis, MongoDB with one click
- **Rollbacks** — instant rollback to any previous deployment
- **Runtime logs** — live streaming from all containers
- **Console** — run one-off commands (migrations, seeds) from the UI
- **System monitor** — CPU, memory, disk, network dashboard
- **S3 backups** — scheduled database backups with retention policies
- **One-click updates** — update StackFly, Caddy, and nixpacks from the UI

## Architecture

```
Internet → Caddy (auto-HTTPS) → App containers
                                      ↑
StackFly binary ──── manages ─────────┘
  ├── SQLite (all state)
  ├── Git bare repos (push-to-deploy)
  ├── Nixpacks (source → Docker image)
  └── Docker (containers, networks, volumes)
```

Single binary. Single server. No Kubernetes. No Docker Compose. Just Docker.

## Requirements

- Linux server (Ubuntu/Debian recommended)
- Tailscale with SSH enabled

Docker and nixpacks are installed automatically by the install script.

## Installation

### 1. Install Tailscale

On your VPS:

```bash
curl -fsSL https://tailscale.com/install.sh | sh
```

### 2. Enable Tailscale SSH

```bash
tailscale up --ssh
```

This connects the server to your tailnet and enables SSH access via Tailscale (no openssh needed).

### 3. Verify connectivity

From your local machine (also on the tailnet), confirm you can reach the server:

```bash
tailscale ip -4   # note the server's Tailscale IP
ssh user@<tailscale-ip>   # should connect without keys
```

### 4. Install StackFly

SSH into the server via Tailscale, then run:

```bash
curl -fsSL https://raw.githubusercontent.com/brhynders/stackfly/master/scripts/install.sh | sudo bash
```

This will:
- Harden the server (remove openssh, configure firewall, kernel hardening, auto-updates)
- Install Docker and nixpacks
- Download the latest StackFly binary
- Configure and start the systemd service

Once complete, access the admin UI at `http://<tailscale-ip>:3000` from any device on your tailnet.

## Usage

### Access the UI

StackFly binds to your Tailscale IP automatically. Open `http://<tailscale-ip>:3000` from any device on your tailnet. No password needed — Tailscale handles auth.

### Deploy an App

1. Create an app in the web UI
2. Add the git remote shown on the app's Overview tab:
   ```bash
   git remote add stackfly <user>@<tailscale-hostname>:/var/lib/stackfly/repos/myapp.git
   ```
3. Push:
   ```bash
   git push stackfly main
   ```

StackFly detects your language, builds a Docker image with Nixpacks, and deploys.

### Procfile

Define your processes in a `Procfile`:

```
release: npm run migrate
web: node server.js
worker: node worker.js
```

- `release` runs once per deploy (before containers start) — use for migrations
- `web` gets routed to via Caddy
- `worker` and other types run as background processes

### Custom Domains

Add domains in the Domains tab. Toggle SSL per domain — when enabled, Caddy auto-provisions a Let's Encrypt certificate (domain must have public DNS pointing to your server).

### Scaling

Set replicas per process type in the Processes tab. Web replicas are automatically load-balanced via Docker DNS round-robin.

### Add-ons

Provision managed databases from the Add-ons tab:

| Add-on | Image | Auto-injected env var |
|--------|-------|----------------------|
| PostgreSQL | postgres:16 | `DATABASE_URL` |
| MySQL | mysql:8 | `DATABASE_URL` |
| Redis | redis:7 | `REDIS_URL` |
| MongoDB | mongo:7 | `MONGODB_URL` |

Connection strings are automatically injected into your app's environment.

### Backups

Configure S3-compatible storage in System → S3 Backup Storage (works with AWS S3, Cloudflare R2, Backblaze B2, MinIO, Wasabi). Then enable per-addon backups with a schedule and retention policy.

### Console

Run one-off commands from the Console tab:

```
npm run migrate
python manage.py createsuperuser
rails db:seed
```

Commands run in a fresh container with your app's image and environment.

## Updating

StackFly checks for new versions automatically. When an update is available, an **Update** button appears in the header. Click it to update the binary, Caddy, and nixpacks — StackFly restarts automatically.

Or from the command line:

```bash
curl -fsSL https://raw.githubusercontent.com/brhynders/stackfly/master/scripts/upgrade.sh | sudo bash
```

## Data

All state lives in `~/.stackfly/` (or `--data-dir`):

- `stackfly.db` — SQLite database (apps, deployments, config)
- `repos/` — git bare repositories
- `caddy/` — generated Caddyfile

App containers, addon data, and Docker images are managed by Docker.

## Development

```bash
# Local dev with Docker (includes all deps)
make dev-up

# Or run directly (requires Go, Docker, nixpacks)
make dev
```

## License

MIT
