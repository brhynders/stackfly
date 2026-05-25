# StackFly

Self-hosted, single-server PaaS. Heroku-like workflow with Git push-to-deploy.

## Build

```
go build -o bin/stackfly ./cmd/stackfly
```

## Run

```
./bin/stackfly --password <admin-password>
```

## Stack

- Go control plane (stdlib net/http, Go 1.23+)
- SQLite via modernc.org/sqlite
- Docker + Docker Compose for app containers
- Caddy (Docker container) for reverse proxy / auto-HTTPS
- Nixpacks for source-to-image builds
- htmx + Tailwind CSS CDN for web UI
