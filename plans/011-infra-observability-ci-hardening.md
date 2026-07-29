# Plan 011: Infra, observability & CI hardening (OWASP sweep 2026-07-28)

> **Executor instructions**: Follow this plan task by task, in order. Run every
> verification command and confirm the expected result before moving on. Each
> task ends in a commit. If anything in "STOP conditions" occurs, stop and
> report — do not improvise. Do **not** update `plans/README.md`; the
> orchestrator maintains the index.
>
> **Drift check (run first)**:
> `git -C backend diff --stat 251107e6..HEAD -- deploy/observability/docker-compose.yml deploy/Caddyfile deploy/docker-compose.prod.yml deploy/backup/ docker-compose.yml Dockerfile .github/workflows/`
> and `git diff --stat 8b3c9e856..HEAD -- .gitignore .github/workflows/ cli/.gitignore mcp/.gitignore HETZNER_DEPLOY.md`
> from the repo root, and `git -C frontend diff --stat ce2df9c..HEAD -- Dockerfile .github/workflows/ .gitignore`.
> On any mismatch with a "Current state" excerpt, treat it as a STOP condition.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW (config-only changes; each is reversible)
- **Depends on**: plan 008 (secrets rotation) should land first — Task 1 rotates
  the Grafana password and touches env handling.
- **Category**: security / infra
- **Planned at**: root `8b3c9e856`, backend `251107e6`, frontend `ce2df9c`, 2026-07-28

## Why this matters

The sweep verified that the production edge (Caddy-only port exposure,
segmented networks, non-root images, boot-time secret guards) is well built,
but the **observability plane is wide open**: every telemetry port is published
on `0.0.0.0` with zero auth and a hardcoded Grafana password — if the host
firewall has any gap, all platform logs are readable and forgeable. Behind
that: IP-spoofable forwarded headers at Caddy, an unpinned CI supply chain,
missing workflow permission scoping, and a LAN-exposed dev database.

## Tasks

### Task 1: Localhost-bind the observability stack + externalize the Grafana password (HIGH)

**Files:**
- Modify: `backend/deploy/observability/docker-compose.yml` (port blocks ~:17-62, Grafana env ~:73-74)
- Modify: `HETZNER_DEPLOY.md` (observability section — document SSH-tunnel access)

Current state: Prometheus `:9090`, Alertmanager `:9093`, Loki `:3100`
(unauthenticated push **and** query), Tempo `:3200`, Alloy `:12345`/`:4317`/`:4318`
all published to `0.0.0.0` with no auth layer in any of their config files;
`GF_SECURITY_ADMIN_USER=admin` / `GF_SECURITY_ADMIN_PASSWORD=reevit` committed.

- [ ] **Step 1: read the compose file + the observability section of
  `HETZNER_DEPLOY.md`** (it flags the Grafana password as unfixed — align with
  its guidance on how operators reach Grafana).
- [ ] **Step 2: edit the port blocks** — for `alloy`, `prometheus`,
  `alertmanager`, `loki`, `tempo`, and `grafana`, rewrite every published port
  as loopback-only, e.g.:

```yaml
  loki:
    # ... unchanged config ...
    ports:
      - "127.0.0.1:3100:3100"  # was "3100:3100" — push+query has no auth
```

  Keep Grafana on `127.0.0.1:3000:3000` as well and document SSH-tunnel access
  in `HETZNER_DEPLOY.md`:

```bash
ssh -L 3000:127.0.0.1:3000 reevit@<host>   # then open http://localhost:3000
```

  (Alloy's OTLP receivers `:4317/:4318` only need container-network reachability
  — they can drop the `ports:` block entirely and stay on the compose network.
  Verify api/worker reach them by service name after the change:
  `docker compose -f backend/deploy/observability/docker-compose.yml config`
  and, if the app services are on the same compose project, no app change is
  needed. If api/worker run in a DIFFERENT compose project, keep
  `127.0.0.1:4317:4317` / `127.0.0.1:4318:4318` published on loopback.)
- [ ] **Step 3: externalize the Grafana password**:

```yaml
    environment:
      - GF_SECURITY_ADMIN_USER=admin
      - GF_SECURITY_ADMIN_PASSWORD=${GRAFANA_ADMIN_PASSWORD:?set GRAFANA_ADMIN_PASSWORD in the stack .env}
```

  Add `GRAFANA_ADMIN_PASSWORD=` to the env template used by that stack (the
  `.env.example` beside the compose file, or the deploy doc's env block — match
  what operators actually copy). The `:?` form fails `docker compose up` fast
  when unset.
- [ ] **Step 4: verify** — `docker compose -f backend/deploy/observability/docker-compose.yml config | grep -B1 -A3 published`
  shows only `127.0.0.1` bindings; `docker compose ... config` errors when
  `GRAFANA_ADMIN_PASSWORD` is unset.
- [ ] **Step 5: commit** — `git commit -am "fix(deploy): localhost-bind observability stack and externalize Grafana password"`

### Task 2: Restrict Caddy trusted proxies to Cloudflare ranges (MEDIUM)

**Files:**
- Modify: `backend/deploy/Caddyfile:19`

Current state: `trusted_proxies static 0.0.0.0/0` — any direct-to-origin
request can spoof `X-Forwarded-For`, defeating the backend's trusted-proxy-aware
rate limiting and poisoning audit IPs. (The Hetzner firewall already restricts
:80/:443 to Cloudflare ranges per `HETZNER_DEPLOY.md` — verify that section
before relying on it; this task removes the single-layer dependency.)

- [ ] **Step 1: implement** — replace the line with the published ranges
  (refresh from https://www.cloudflare.com/ips-v4 and /ips-v6 at execution):

```caddyfile
	servers {
		# Cloudflare egress ranges only (https://www.cloudflare.com/ips — refresh
		# periodically; the host firewall already restricts 80/443 to these).
		trusted_proxies static 173.245.48.0/20 103.21.244.0/22 103.22.200.0/22 103.31.4.0/22 141.101.64.0/18 108.162.192.0/18 190.93.240.0/20 188.114.96.0/20 197.234.240.0/22 198.41.128.0/17 162.158.0.0/15 104.16.0.0/13 104.24.0.0/14 172.64.0.0/13 131.0.72.0/22 2400:cb00::/32 2606:4700::/32 2803:f800::/32 2405:b500::/32 2405:8100::/32 2a06:98c0::/29 2c0f:f248::/32
	}
```

- [ ] **Step 2: verify** — `docker run --rm -v "$PWD/backend/deploy/Caddyfile:/etc/caddy/Caddyfile" caddy:2-alpine caddy validate --config /etc/caddy/Caddyfile`
  → `Valid configuration`.
- [ ] **Step 3: commit** — `git commit -am "fix(deploy): trust forwarded headers only from Cloudflare ranges"`

### Task 3: Pin CI actions to SHAs and tools to versions (MEDIUM)

**Files:**
- Modify: `backend/.github/workflows/ci.yml`, `backend/.github/workflows/smoke.yml`
- Modify: `frontend/.github/workflows/ci.yml` (if present)
- Modify: `.github/workflows/sdk-docs.yml`
- Modify: any `backend/.github/workflows/` + root release workflows
- Modify: `backend/Dockerfile` (~:16, :22 — `goose@latest`)

Current state: all actions use mutable tags; CI installs errcheck, ineffassign,
govulncheck, goose `@latest`; the production Dockerfile bakes `goose@latest`.

- [ ] **Step 1: pin actions** — replace tags with SHAs resolved 2026-07-28
  (keep the tag as a trailing comment):

```yaml
- uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5 # v4.3.1
- uses: actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff # v5.6.0
- uses: actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020 # v4.4.0
- uses: docker/build-push-action@10e90e3645eae34f1e60eeb005ba3a3d33f178e8 # v6.19.2
- uses: docker/login-action@c94ce9fb468520275223c153574b00df6fe4bcc9 # v3.7.0
- uses: codecov/codecov-action@0f8570b1a125f4937846a11fcfa3bcd548bd8c97 # v4.6.0
- uses: goreleaser/goreleaser-action@e435ccd777264be153ace6237001ef4d979d3a7a # v6.4.0
```

  For `dtolnay/rust-toolchain@stable` (sdk-docs.yml:25) resolve at execution:

```bash
git ls-remote https://github.com/dtolnay/rust-toolchain refs/tags/stable
# then: dtolnay/rust-toolchain@<sha> # stable (resolved 2026-07-28)
```

  Any other action found while editing: same pattern
  (`git ls-remote https://github.com/<owner>/<repo> refs/tags/<tag>`).
- [ ] **Step 2: pin Go tools** in `backend/.github/workflows/ci.yml`:

```yaml
- run: go install github.com/kisielk/errcheck@v1.9.1        # was @latest
- run: go install github.com/gordonklaus/ineffassign@v0.2.0 # was @latest
- run: go install golang.org/x/vuln/cmd/govulncheck@v1.6.0  # was @latest
- run: go install github.com/pressly/goose/v3/cmd/goose@v3.27.3 # was @latest
```

  (Confirm the errcheck/ineffassign tags exist via
  `git ls-remote --tags https://github.com/kisielk/errcheck` /
  `.../gordonklaus/ineffassign`; use the newest tag printed if the ones above
  don't resolve.)
- [ ] **Step 3: pin goose in `backend/Dockerfile`** — change both install lines
  to `goose@v3.27.3` (matches the compose `GOTOOLCHAIN=local` constraint; the
  install must succeed with the image's Go — verify with a local build).
- [ ] **Step 4: verify** — `docker build -f backend/Dockerfile backend` succeeds
  (or at minimum the goose install layer); CI run on the PR is green.
- [ ] **Step 5: commit** — `git commit -am "ci: pin actions to SHAs and Go tools to exact versions"`

### Task 4: Explicit workflow `permissions:` blocks (MEDIUM)

**Files:**
- Modify: `backend/.github/workflows/ci.yml`, `frontend/.github/workflows/ci.yml`, `.github/workflows/sdk-docs.yml`

- [ ] **Step 1: add at workflow level** (top-level key, after `on:`):

```yaml
permissions:
  contents: read
```

- [ ] **Step 2: preserve job-level needs** — read each workflow fully; any job
  that pushes releases/images must keep its own `permissions:` (e.g.
  `contents: write`, `id-token: write`, `packages: write`) at job level — job
  level overrides workflow level, so the top-level stays `contents: read`.
  `smoke.yml` already scopes correctly (use it as the reference shape).
- [ ] **Step 3: verify** — `actionlint` if installed (`actionlint .github/workflows/*.yml backend/.github/workflows/*.yml`), else CI green on the PR.
- [ ] **Step 4: commit** — `git commit -am "ci: scope GITHUB_TOKEN to contents:read by default"`

### Task 5: Localhost-bind dev-compose services (MEDIUM)

**Files:**
- Modify: `backend/docker-compose.yml:22-23` (+ any other published ports in that file)

Current state: `"5433:5432"` publishes Postgres (creds `reevit`/`reevit` per
`config.yaml:15-16`) on `0.0.0.0` — any LAN peer can reach a developer's DB.

- [ ] **Step 1: read the file**, then change every dev-service `ports:` entry
  (postgres, redis, and any others you find — e.g. a published API port is fine
  to keep on localhost too):

```yaml
    ports:
      - "127.0.0.1:5433:5432"
```

- [ ] **Step 2: verify** — `docker compose -f backend/docker-compose.yml config | grep -A2 published`
  shows `127.0.0.1` only; `TEST_DATABASE_URL='postgres://reevit:reevit@localhost:5433/reevit_test?sslmode=disable' go test ./internal/infra/...`
  still connects (loopback unchanged for the developer).
- [ ] **Step 3: commit** — `git commit -am "fix(dev): localhost-bind dev compose service ports"`

### Task 6: Document the prod Postgres `sslmode=disable` risk decision (MEDIUM)

**Files:**
- Modify: `HETZNER_DEPLOY.md` (database section)

The backend image reads env directly (no `*_FILE` support verified in
`internal/infra/config`) and Postgres/Redis have no host ports on the single
VPS — internal TLS on the docker bridge is poor cost/benefit today. This task
records the decision explicitly instead of leaving it implicit.

- [ ] **Step 1: add to `HETZNER_DEPLOY.md`** in the database/deploy section:

```markdown
### Accepted risk: plaintext DB traffic on the docker bridge

`DATABASE_URL` uses `sslmode=disable` between api/worker/migrate/backup and the
co-located Postgres. Postgres publishes no host port and the `data` network is
internal to the single VPS, so exposure requires host compromise — at which
point TLS on the same host adds nothing. Revisit if the DB ever moves off-host
(then `sslmode=require` + a generated CA becomes mandatory, not optional).
```

- [ ] **Step 2: commit** — `git commit -am "docs(deploy): record accepted risk for internal DB transport encryption"`

### Task 7: Close the `.gitignore` gaps that enabled the `.env` incident (LOW)

**Files:**
- Modify: `.gitignore` (root — currently only `node_modules`, `__pycache__/`, `.DS_Store`)
- Modify: `cli/.gitignore`, `mcp/.gitignore`, `frontend/.gitignore`

- [ ] **Step 1: append** to root, `cli/.gitignore`, and `mcp/.gitignore`:

```gitignore
# Local secrets — never commit env files
.env
.env.*
!.env.example
```

  In `frontend/.gitignore`: it already covers `.env`/`.env.local` — add
  `.env.*` + `!.env.example` if absent (read it first).
- [ ] **Step 2: verify** — from each of root/, cli/, mcp/, frontend/:
  `git check-ignore -v .env` prints a matching rule; `git check-ignore .env.example`
  exits non-zero (NOT ignored).
- [ ] **Step 3: commit** — `git commit -am "chore: gitignore .env files repo-wide (example files excepted)"`

### Task 8: api/worker healthchecks in prod compose (LOW)

**Files:**
- Modify: `backend/deploy/docker-compose.prod.yml` (~:169 api, ~:190 worker)
- Modify: `backend/Dockerfile` (final stage — only if option (b) is chosen)

Current state: healthchecks exist for postgres/redis/frontend only; the prod
image is static binaries + ca-certificates (no shell, no wget). The Go API
exposes a health endpoint (the frontend hits `/healthz` — confirm the exact
path in `cmd/api/main.go`).

- [ ] **Step 1: pick the probe** — read `backend/Dockerfile`'s final stage.
  Option (a), lightest: copy a static `wget` from a pinned busybox image into a
  stage that already exists, without adding a shell to the runtime:

```dockerfile
FROM busybox:1.36.1@sha256:<resolve-at-execution> AS busybox
# ... in the final stage:
COPY --from=busybox /bin/wget /usr/bin/wget
```

  (Resolve the digest: `docker buildx imagetools inspect busybox:1.36.1`.)
  Option (b): if a static `wget` copy feels wrong for the distroless image, add
  a tiny `cmd/healthcheck` Go binary built in the existing builder stage and
  `HEALTHCHECK`-friendly. Pick (a) unless the Dockerfile structure says otherwise.
- [ ] **Step 2: add healthchecks**:

```yaml
  api:
    # ...
    healthcheck:
      test: ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
```

  Worker: use the worker's own liveness signal (check `cmd/worker` for a health
  port; if it has none, use `test: ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/healthz"]`
  only for api and give worker a `pgrep`-free alternative — if no probe is
  possible, document that and skip worker in this task).
- [ ] **Step 3: verify** — `docker compose -f backend/deploy/docker-compose.prod.yml config`
  valid; image builds.
- [ ] **Step 4: commit** — `git commit -am "fix(deploy): add api healthcheck to production compose"`

### Task 9: Pin prod-facing images by digest (LOW)

**Files:**
- Modify: `backend/deploy/docker-compose.prod.yml` (postgres, redis, caddy)
- Modify: `backend/deploy/observability/docker-compose.yml` (grafana, alloy, prometheus, loki, tempo, alertmanager)
- Modify: `frontend/Dockerfile` (`node:24-slim`, both stages)
- Modify: `backend/docker-compose.yml` (dev images — same treatment, lower stakes)

- [ ] **Step 1: resolve digests at execution** (network required):

```bash
for img in postgres:16-alpine redis:7-alpine caddy:2-alpine node:24-slim grafana/grafana:11.1.0 grafana/alloy:v1.14.0 prom/prometheus:latest grafana/loki:latest grafana/tempo:latest grafana/alertmanager:latest; do
  docker buildx imagetools inspect "$img" --format '{{.Manifest.Digest}}'
done
```

  (Use the tags actually present in each file — read them first; `latest` is a
  smell worth pinning to the current major tag while you're there.)
- [ ] **Step 2: rewrite** each as `image: <name>@sha256:<digest>  # <tag>` —
  the comment preserves the human-readable pin.
- [ ] **Step 3: verify** — both compose files pass `docker compose config`;
  `docker build -f frontend/Dockerfile frontend` succeeds.
- [ ] **Step 4: commit** — `git commit -am "chore(deploy): pin production-facing container images by digest"`

### Task 10: Backup credential handling (LOW)

**Files:**
- Modify: `backend/deploy/backup/backup.sh:23`
- Modify: `HETZNER_DEPLOY.md` (backup section)

Current state: full `DATABASE_URL` (with password) is passed as a `pg_dump`
argument — visible in the container's process list during the dump window.

- [ ] **Step 1: read `backup.sh` and its container setup**, then switch to a
  pgpass file:

```bash
# near the top of backup.sh
install -m 0600 /dev/stdin "$HOME/.pgpass" <<EOF
${PGHOST:-postgres}:${PGPORT:-5432}:*:${POSTGRES_USER}:${POSTGRES_PASSWORD}
EOF

# and the dump call drops the DSN:
pg_dump --host="${PGHOST:-postgres}" --username="${POSTGRES_USER}" --dbname="${POSTGRES_DB}" ...
```

  (Match the script's real variable names — read it; keep the R2 keys as env
  vars per S3-tooling convention.)
- [ ] **Step 2: doc note** in `HETZNER_DEPLOY.md`: R2/VAULT/PASETO secrets are
  visible via `docker inspect` on the host — acceptable for the single-operator
  VPS; moving to docker secrets is the documented future hardening.
- [ ] **Step 3: verify** — run the backup script once against the dev stack;
  `ps aux` during the run shows no password in the `pg_dump` args.
- [ ] **Step 4: commit** — `git commit -am "fix(deploy): keep DB password out of the backup process list"`

## STOP conditions

- Any port/removal change in Task 1 breaks OTLP ingestion from api/worker →
  revert the Alloy port drop to loopback-published (`127.0.0.1:4317:4317`,
  `127.0.0.1:4318:4318`) and continue; do not leave telemetry silently dark.
- Task 3: a pinned tool version fails to install with the repo's Go toolchain →
  pick the nearest older tag that installs, note the substitution in the commit.
- Task 8: the worker has no probeable liveness signal → ship the api
  healthcheck only and record the gap in the commit message.
- You do not have registry network access for Tasks 8-9 digest resolution →
  resolve-at-execution steps stay unchecked; commit the rest separately and
  leave a note in the task.

## Verification (end of plan)

- `docker compose config` valid for all three compose files; only `127.0.0.1`
  port bindings outside Caddy `:80/:443`.
- `GRAFANA_ADMIN_PASSWORD` unset → observability stack refuses to start.
- Caddy config validates; no `0.0.0.0/0` trusted proxies remain.
- Every workflow action pinned to a 40-char SHA; every workflow has a
  top-level `permissions:` block; no `@latest` installs remain in CI or the
  backend Dockerfile.
- `git check-ignore .env` matches in root, cli, mcp, frontend.
