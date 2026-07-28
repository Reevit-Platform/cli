# Deploying Reevit to a single Hetzner VPS

End-to-end runbook: one Hetzner Cloud server behind Cloudflare, running the API,
the worker, Postgres, Redis, the frontend, and offsite backups. Target cost is
**≈ €9–12/month all-in**.

Files this guide uses (all committed in the backend repo):

| File | Purpose |
| --- | --- |
| `backend/deploy/docker-compose.prod.yml` | The production stack |
| `backend/deploy/Caddyfile` | TLS termination + routing |
| `backend/deploy/env.prod.example` | Template for `.env.prod` |
| `backend/deploy/backup/` | 6-hourly `pg_dump` → R2 |
| `backend/docs/RECOVERY_RUNBOOK.md` | Restore contract and drill |
| `backend/docs/environment-variables.md` | Full config reference |

---

## Architecture

```
                       Cloudflare (DNS · proxy · WAF · Access)
                                      │
                          ┌───────────┴───────────┐
                          │  api.example.com      │
                          │  example.com / www    │
                          └───────────┬───────────┘
                                      │  origin TLS (Cloudflare Origin CA)
┌─────────────────────────────────────┴─────────────────────────────────────┐
│ Hetzner CX32 · 4 vCPU · 8 GB                                              │
│                                                                           │
│  ┌── edge network ──────────────────────┐  ┌── data network ───────────┐  │
│  │  caddy :80 :443                      │  │  postgres 16              │  │
│  │  frontend :3000  (all surfaces)      │  │  redis 7 (AOF)            │  │
│  │  api :8080 ──────────────────────────┼──┤  worker (asynq + cron)    │  │
│  └──────────────────────────────────────┘  │  migrate (one-shot)       │  │
│                                            │  backup → R2              │  │
│                                            └───────────────────────────┘  │
└───────────────────────────────────────────────────────────────────────────┘
        Only :80/:443 are published. Postgres and Redis have no host port.
```

**Two rules this design is built around.** Images are **built in CI and only
pulled** on the host — a Go build plus `pnpm build` peaks past 4 GB and will
OOM-kill Postgres mid-transaction. And migrations run in **exactly one**
one-shot service, because the production image's `ENTRYPOINT` runs `goose up`
unconditionally; `api` and `worker` each override `entrypoint` to skip it.

---

## Read this before you start

Three properties of the current code shape the deployment. None are blockers,
but each one silently breaks something if you assume otherwise.

**1. All surfaces live on one origin, path-based.** The frontend's routes are
`src/routes/index.tsx`, `src/routes/dashboard/`, `src/routes/platform/`,
`src/routes/pay/`, and neither `server.mjs` nor the router maps hostnames to
route prefixes. So:

```
https://example.com/              marketing
https://example.com/dashboard     merchant app
https://example.com/platform      operator console
https://example.com/pay/<code>    payment links
```

`dashboard.example.com` would render the *marketing* page. Do not try to fix
that with a Caddy `rewrite` — the router emits absolute paths, so the prefix
gets applied twice on the first internal link. A real subdomain split needs
host-aware route resolution in the app; it is a follow-up, not a deploy step.

**2. `frontend/src/server/proxy.ts` hardcodes `reevit.io`.** Line 97 gates the
shared-cookie-domain rewrite on `hostname === "reevit.io" || endsWith(".reevit.io")`.
Harmless on a single origin (host-only cookies work fine, and `COOKIE_DOMAIN`
is empty in the template), but it must be parameterised before any
multi-subdomain deployment.

**3. The API refuses to boot in production without three security vars.**
`internal/infra/config/config.go` hard-fails on missing `FRONTEND_ALLOWED_ORIGINS`,
`REQUIRE_2FA_FOR_ADMIN=true`, or `METRICS_AUTH_TOKEN` — and on a `VAULT_KEY` or
`PASETO_KEY` that is the committed default, under 32 bytes of material, or has
fewer than 8 distinct bytes. `cmd/worker` shares the same validation, so **both**
services need all of them.

---

## Phase 1 — Publish images from CI

Nothing else works until there are images to pull. Both repos currently build in
CI without pushing (`frontend/.github/workflows/ci.yml` has `push: false`, and
the backend has no image job at all).

### Backend — `.github/workflows/publish-image.yml`

```yaml
name: publish-image

on:
  push:
    branches: [dev, main]
  workflow_dispatch:

permissions:
  contents: read
  packages: write

jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-buildx-action@v3

      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      # GHCR rejects uppercase paths, so the owner is written out in lowercase
      # rather than interpolated from github.repository.
      - uses: docker/build-push-action@v6
        with:
          context: .
          target: production
          platforms: linux/amd64
          push: true
          tags: ghcr.io/reevit-platform/backend:sha-${{ github.sha }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

### Frontend — add a push job alongside the existing build check

`VITE_*` are baked at build time, so the public origins are decided here, not on
the server.

```yaml
name: publish-image

on:
  push:
    branches: [dev, main]
  workflow_dispatch:

permissions:
  contents: read
  packages: write

jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-buildx-action@v3

      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - uses: docker/build-push-action@v6
        with:
          context: .
          platforms: linux/amd64
          push: true
          tags: ghcr.io/reevit-platform/frontend-start:sha-${{ github.sha }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
          build-args: |
            VITE_API_URL=https://api.example.com
            VITE_DASHBOARD_URL=https://example.com/dashboard
            VITE_PLATFORM_URL=https://example.com/platform
            VITE_PAYMENT_LINK_URL=https://example.com/pay
            VITE_APP_VERSION=${{ github.sha }}
            VITE_VERCEL_ENV=production
```

Then, in each repo's **Settings → Packages**, set a retention policy keeping the
last ~5 versions. Your images are roughly 150 MB (Go) and 400 MB (frontend);
unpruned history will quietly consume the free Packages allowance.

> **Zero-cost alternative to GHCR:** build locally or in CI, then
> `docker save ghcr.io/... | ssh reevit@HOST docker load`. No registry, no
> storage bill, no login on the host.

---

## Phase 2 — Create the server

Hetzner Cloud console → **New project** → add your SSH public key first.

| Setting | Value |
| --- | --- |
| Type | **CX32** — 4 vCPU / 8 GB / 80 GB (≈ €7/mo) |
| Location | Falkenstein or Nuremberg |
| Image | Ubuntu 24.04 |
| Backups | **Enable** (+20%, ≈ €1.40/mo) |
| Networking | IPv4 + IPv6 |
| SSH key | yours — do not use a root password |

Why 8 GB when the app needs ~2.5 GB: it leaves room to self-host observability
later without a migration, and it guarantees the database is never the thing the
kernel picks to OOM-kill.

On latency from Ghana, Falkenstein is ~130–160 ms and Johannesburg is not
reliably better — West African cables land in Europe. Cloudflare has an Accra
PoP, so client TLS terminates locally and only the origin fetch crosses.

### Cloud firewall

Create a firewall in the Hetzner console and attach it to the server:

| Direction | Port | Source |
| --- | --- | --- |
| in | 22 | **your IP only** |
| in | 80, 443 | Cloudflare ranges — [IPv4](https://www.cloudflare.com/ips-v4) · [IPv6](https://www.cloudflare.com/ips-v6) |

Restricting 80/443 to Cloudflare is **not optional here**: the origin serves a
Cloudflare Origin CA certificate, which only Cloudflare trusts, and it is what
stops anyone bypassing your WAF by hitting the IP directly.

---

## Phase 3 — Prepare the host

```bash
ssh root@YOUR_SERVER_IP
```

```bash
apt update && apt upgrade -y
apt install -y ca-certificates curl gnupg fail2ban unattended-upgrades
```

**Swap** — cheap insurance against a Node SSR spike killing Postgres:

```bash
fallocate -l 4G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
echo '/swapfile none swap sw 0 0' >> /etc/fstab
```

**Kernel settings Redis and Postgres both want:**

```bash
cat >> /etc/sysctl.d/99-reevit.conf <<'EOF'
vm.overcommit_memory = 1
vm.swappiness = 10
net.core.somaxconn = 1024
EOF
sysctl --system
```

`vm.overcommit_memory=1` matters specifically because Redis forks to rewrite its
AOF; without it that fork can fail under memory pressure and you lose queue
durability exactly when the box is busiest.

**Docker, from the official repo:**

```bash
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" > /etc/apt/sources.list.d/docker.list
apt update
apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
```

**Deploy user** (the stack should not run as root):

```bash
adduser --disabled-password --gecos "" reevit
usermod -aG docker reevit
mkdir -p /home/reevit/.ssh
cp /root/.ssh/authorized_keys /home/reevit/.ssh/
chown -R reevit:reevit /home/reevit/.ssh && chmod 700 /home/reevit/.ssh
mkdir -p /opt/reevit && chown reevit:reevit /opt/reevit
```

**Lock down SSH** — verify `ssh reevit@HOST` works in a second terminal *before*
running this:

```bash
sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin no/' /etc/ssh/sshd_config
sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
systemctl restart ssh
```

---

## Phase 4 — Ship the deploy files

From your workstation, in the `primeflow` checkout:

```bash
rsync -av --exclude 'observability' backend/deploy/ reevit@YOUR_SERVER_IP:/opt/reevit/
```

That lands `docker-compose.prod.yml`, `Caddyfile`, and `backup/` in
`/opt/reevit/`. The compose file's relative paths (`./Caddyfile`, `./certs`,
`./backup`) resolve from there, so **run every compose command from
`/opt/reevit`**.

---

## Phase 5 — Secrets

On the server:

```bash
cd /opt/reevit
cp env.prod.example .env.prod
chmod 600 .env.prod
```

Generate the values:

```bash
openssl rand -base64 32   # POSTGRES_PASSWORD
openssl rand -base64 32   # VAULT_KEY
openssl rand -base64 32   # PASETO_KEY
openssl rand -hex 32      # METRICS_AUTH_TOKEN
```

> **Back up `VAULT_KEY` off this server before first boot.** It encrypts stored
> PSP credentials. Lose it and every payment connection in the database becomes
> undecryptable — and there is no AES→AES rotation path. See
> `backend/docs/SECRET_ROTATION.md`.

Then edit `.env.prod`: set `APEX_DOMAIN`, `DASHBOARD_BASE_URL`,
`FRONTEND_ALLOWED_ORIGINS`, the image tags from Phase 1, and whichever PSP /
email / SMS keys you use (`backend/docs/environment-variables.md` is the full
list). Leave `OTEL_ENABLED=false` for now.

**Log in to GHCR** (needed only for private packages):

```bash
echo "YOUR_GITHUB_PAT_WITH_read:packages" | docker login ghcr.io -u YOUR_GH_USER --password-stdin
```

---

## Phase 6 — Cloudflare DNS and origin certificate

**DNS** — in the Cloudflare dashboard for your zone, all **proxied** (orange):

| Type | Name | Content |
| --- | --- | --- |
| A | `@` | `YOUR_SERVER_IP` |
| A | `www` | `YOUR_SERVER_IP` |
| A | `api` | `YOUR_SERVER_IP` |

**SSL/TLS → Overview → set mode to "Full (strict)".** Anything less leaves the
origin leg unencrypted or unverified.

**SSL/TLS → Origin Server → Create Certificate.** Accept the defaults (RSA,
15-year). Copy the two blocks onto the server:

```bash
mkdir -p /opt/reevit/certs
nano /opt/reevit/certs/origin.pem   # paste the certificate
nano /opt/reevit/certs/origin.key   # paste the private key
chmod 600 /opt/reevit/certs/origin.key
```

An Origin CA cert avoids ACME entirely — no HTTP-01 challenge through
Cloudflare's proxy, no Let's Encrypt rate limits while you iterate, no renewal
job to fail at 3am. The tradeoff is that only Cloudflare trusts it, which is
why the Phase 2 firewall rule is mandatory.

---

## Phase 7 — First boot

```bash
cd /opt/reevit
docker compose -f docker-compose.prod.yml --env-file .env.prod pull
docker compose -f docker-compose.prod.yml --env-file .env.prod up -d
```

Watch the ordering — Postgres becomes healthy, `migrate` runs to completion and
exits 0, then `api` and `worker` start:

```bash
docker compose -f docker-compose.prod.yml --env-file .env.prod ps
docker compose -f docker-compose.prod.yml --env-file .env.prod logs -f migrate api worker
```

**If `api` exits immediately**, read its logs first — the most likely cause is
the production config guard, and the error names exactly what is missing:

```
missing required production configuration: FRONTEND_ALLOWED_ORIGINS, METRICS_AUTH_TOKEN
insecure production secret keys: PASETO_KEY must be at least 32 bytes of key material
```

### Verify

```bash
# App and API, end to end through Cloudflare
curl -sS https://example.com/healthz            # -> ok
curl -sS https://api.example.com/healthz        # -> {"status":"ok", ...}

# /metrics must NOT be publicly reachable
curl -so /dev/null -w '%{http_code}\n' https://api.example.com/metrics   # -> 404

# ...but works internally with the token
docker compose -f docker-compose.prod.yml --env-file .env.prod exec api \
  /bin/sh -c 'echo ok'   # container is alive

# Datastores must have no host port
ss -tlnp | grep -E '5432|6379'                  # -> no output

# Memory headroom
docker stats --no-stream
free -h
```

**Test SSE explicitly** — it is the thing most likely to be silently broken by a
reverse proxy, and it fails as "the live feed just never updates" rather than as
an error. Sign in, then:

```bash
curl -N -H 'Cookie: <your session cookie>' https://api.example.com/v1/events/stream
```

You should see frames arrive incrementally. If it connects and then hangs, the
`flush_interval -1` in the Caddyfile is not taking effect.

---

## Phase 8 — Backups, then prove they restore

Create an R2 bucket, then an R2 API token scoped **write-only** to it. Add a
lifecycle rule expiring objects after 90 days — that is how retention is
enforced, rather than granting the job delete rights.

Fill in `BACKUP_BUCKET`, `S3_ENDPOINT`, `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, and a `BACKUP_HEALTHCHECK_URL` (healthchecks.io free
tier), then:

```bash
docker compose -f docker-compose.prod.yml --env-file .env.prod --profile backup up -d --build backup
docker compose -f docker-compose.prod.yml --env-file .env.prod logs -f backup
```

The first dump fires immediately on start — deliberately, so bad R2 credentials
surface now instead of in six hours. After that it sleeps 6 hours between runs.

**Then run the restore drill in `backend/docs/RECOVERY_RUNBOOK.md`.** An
untested backup is not a backup, and at a 6-hour interval your RPO is up to six
hours of payments. Decide explicitly whether that is acceptable; if not, shorten
the interval in the `backup` service's entrypoint.

---

## Phase 9 — Observability

Self-hosting the full stack (`backend/deploy/observability/`) costs ~2 GB of RAM
and puts Loki compaction and Prometheus TSDB writes on the same disk your
payments Postgres is fsyncing to. On one box, **don't**.

Instead, point telemetry at Grafana Cloud's free tier and set in `.env.prod`:

```bash
OTEL_ENABLED=true
OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-<region>.grafana.net/otlp
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic <base64 instance:token>
OTEL_EXPORTER_OTLP_INSECURE=false
OTEL_SAMPLE_RATE=0.1
```

```bash
docker compose -f docker-compose.prod.yml --env-file .env.prod up -d api worker
```

Keep the sample rate low — traces are the largest line item on any hosted
backend, and 10% is ample for spotting a failing PSP route.

If you later self-host instead, the stack in `deploy/observability/` is
loopback-bound end-to-end (2026-07-28): every read/query surface listens on
`127.0.0.1`, so reach Grafana and Prometheus via SSH tunnel:

```bash
ssh -L 3001:127.0.0.1:3001 -L 9090:127.0.0.1:9090 reevit@YOUR_SERVER_IP
# Grafana → http://localhost:3001   Prometheus → http://localhost:9090
```

Two things must be set first (both are enforced — the stack refuses to start
without them):

```bash
GRAFANA_ADMIN_PASSWORD=<openssl rand -hex 16>   # no committed default anymore
METRICS_AUTH_TOKEN=<same value as the api service>  # Prometheus scrape auth
```

The only non-loopback ports are Alloy's OTLP receivers (`4317`/`4318`,
write-only telemetry ingestion for the api/worker containers) — Loki, Tempo,
Prometheus, and Alertmanager accept nothing off-loopback. In prod the api
publishes no host port, so point the `reevit-api` scrape at a shared docker
network (attach the prometheus container to the prod `edge` network and target
`api:8080`) instead of `host.docker.internal`.

---

## Routine operations

**Deploy a new version.** Update the tags, pull, recreate:

```bash
cd /opt/reevit
sed -i 's/^BACKEND_TAG=.*/BACKEND_TAG=sha-NEWSHA/'  .env.prod
sed -i 's/^FRONTEND_TAG=.*/FRONTEND_TAG=sha-NEWSHA/' .env.prod
docker compose -f docker-compose.prod.yml --env-file .env.prod pull
docker compose -f docker-compose.prod.yml --env-file .env.prod up -d
```

`migrate` re-runs to completion on every `up`, and `api`/`worker` wait on it.
This is why tags are pinned to SHAs — `latest` makes rollback guesswork and
makes "what is actually running?" unanswerable.

**Roll back.** Put the previous SHA back and repeat. Note that a rollback does
**not** revert migrations, so a schema change must be backward-compatible with
the previous image for one release. New migrations must be numbered `000130+`
(the shared dev/test DBs carry phantom goose rows for 118–125 and 129).

**Logs and disk:**

```bash
docker compose -f docker-compose.prod.yml --env-file .env.prod logs -f --tail 100 api
df -h && docker system df
docker image prune -af --filter 'until=168h'
```

Every service caps json-file logs at 3 × 10 MB, so logs cannot fill the disk —
but old images will, so prune on a schedule.

**Postgres shell:**

```bash
docker compose -f docker-compose.prod.yml --env-file .env.prod exec postgres \
  psql -U reevit -d reevit
```

---

## Known constraints

- **The worker cannot be scaled past one replica.** It runs the cron scheduler
  in-process (`cmd/worker/scheduler.go`), so a second replica double-fires every
  entry — duplicate renewals, duplicate invoices. Scaling it needs leader
  election first.
- **`api` has no container healthcheck.** The `production` image installs only
  `ca-certificates` — no `curl`, no `wget`, and no shell-reachable probe. Adding
  `curl` to the production stage (or a tiny Go healthcheck binary) would let
  Docker restart a wedged API on its own. Until then, Cloudflare and your own
  uptime check are the detection path.
- **Single point of failure.** One box, one Postgres, no replica. The mitigation
  is the backup job plus a *rehearsed* restore — not hope. Hetzner snapshots
  cover host loss; they do not cover a bad migration.
- **The subdomain split is app work**, not configuration. See the note at the
  bottom of `docker-compose.prod.yml`.
