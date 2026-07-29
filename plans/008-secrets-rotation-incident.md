# Plan 008: Secrets rotation incident — execute SECRET_ROTATION.md and close the git-history leak (OWASP sweep 2026-07-28)

> **Executor instructions**: This is an **OPERATOR (HITL) plan**. Every task
> requires production/console access and is executed by a human operator, not an
> agent. Agents dispatched here: STOP immediately and hand back to the
> maintainer. Do the tasks in order — **rotate first, scrub history second,
> force-push last**. If anything in "STOP conditions" occurs, stop and report.
> When done, update the status row for this plan in `plans/README.md`.
>
> **Drift check (run first)**: in `backend/`, run
> `git show 37f455a8^:.env > /dev/null 2>&1 && echo "LEAK STILL IN HISTORY" || echo "blob gone (verify before planning)"`.
> Also confirm `git ls-files | grep -x '.env'` returns nothing (already untracked).
> If the blob is gone, skip to Task 7 (post-rotation audit) only if rotation is
> confirmed complete for every key in the inventory.

## Status

- **Priority**: P0 (active incident — treat as compromised credentials)
- **Effort**: M (2–4 operator hours + provider console time)
- **Risk**: HIGH if mishandled (PASETO rotation logs out all users; VAULT_KEY
  mishandled = stored PSP credentials permanently undecryptable)
- **Depends on**: none. **Blocks**: plan 011 (Grafana password rotation assumes
  this plan's env handling lands first).
- **Category**: security / incident
- **Planned at**: backend `7892afc1`-era blob confirmed present 2026-07-28; runbook `backend/docs/SECRET_ROTATION.md` already shipped (issue #323 code half)

## Why this matters

`backend/.env` was committed with **live** secrets and later untracked, but the
blob is still in history and retrievable by anyone with clone access:

```bash
git show 37f455a8^:.env   # returns the full file today (verified 2026-07-28)
```

Confirmed present (names only, verified): `PASETO_KEY` (forge any user session),
`VAULT_KEY` (AES-GCM master key — decrypts every stored merchant PSP credential
and TOTP secret), `OAUTH_GOOGLE_CLIENT_SECRET`, `OAUTH_GITHUB_CLIENT_SECRET`,
`RESEND_API_KEY`, `PAYSTACK_BILLING_SECRET`, SMTP/NOTIFIER passwords,
`NOTIFIER_WEBHOOK_SECRET`, `SAIL_RIDES_API_KEY`, OTEL tokens, and more.
The rotation runbook exists (`backend/docs/SECRET_ROTATION.md`) but the
backend changelog records rotation + scrub as a **pending operator task** — it
has not been executed. This plan is the tracker that drives it to done and adds
the post-rotation misuse audit the runbook lacks.

## Current state

1. **Leak verified** — `git show 37f455a8^:.env` returns ~30 secret entries;
   `.env` is not tracked at HEAD (`git ls-files | grep -x '.env'` empty).
2. **Runbook shipped** — `backend/docs/SECRET_ROTATION.md` covers inventory,
   VAULT_KEY migration paths, scrub commands, force-push, and a verify
   checklist. This plan references it instead of duplicating it.
3. **Boot guard shipped** — `config.Validate()` refuses the known-default /
   low-entropy `PASETO_KEY`/`VAULT_KEY` in production, so rotated config fails
   fast on rollback to the leaked values.
4. **Not yet done** (the gap this plan closes): no rotation has been performed,
   history is unscrubbed, no misuse audit has run.

## Tasks

- [ ] **Task 1: Pre-flight (15 min)**

  1. Announce a freeze window in the team channel: no merges to `main` on the
     backend repo until Task 6 completes (history rewrite follows).
  2. Snapshot the inventory (names only — never paste values anywhere):

     ```bash
     cd backend && git show 37f455a8^:.env | grep -oE '^[A-Z_]+=' | sort > /tmp/leaked-keys.txt
     wc -l /tmp/leaked-keys.txt   # expect ~30
     ```

  3. Confirm you have console access for: production host (Hetzner, per
     `HETZNER_DEPLOY.md`), Google Cloud console, GitHub OAuth app settings,
     Resend, Paystack, the Railway/R2 bucket, the OTEL collector, and Sail Rides.
  4. Confirm you can redeploy api + worker (rolling) during the window.

- [ ] **Task 2: Rotate the uncoupled secrets (60 min)**

  For each row below, rotate at the provider, inject into the production env
  file (`/opt/reevit/.env.prod` on the host, `chmod 600` per the deploy doc),
  and tick it off. `PASETO_KEY` first:

  ```bash
  openssl rand -base64 32   # new PASETO_KEY — injecting it invalidates ALL sessions (expected)
  ```

  Then: `OAUTH_GOOGLE_CLIENT_SECRET` (Google Cloud console → Credentials →
  reset secret), `OAUTH_GITHUB_CLIENT_SECRET` (GitHub → Developer settings →
  OAuth Apps → regenerate), `RESEND_API_KEY` (Resend dashboard → revoke +
  create), `PAYSTACK_BILLING_SECRET` (Paystack dashboard → reset secret key —
  also rotates the matching public key; update both), `RAILWAY_BUCKET_*` (bucket
  credentials — note this breaks `deploy/backup` until updated),
  `EMAIL_SMTP_PASSWORD` / `NOTIFIER_EMAIL_PASSWORD`, `NOTIFIER_WEBHOOK_SECRET`,
  `SAIL_RIDES_API_KEY`, OTEL `Authorization` header / `TOKEN=`. Identify
  `NANO_BANANA` from the inventory and rotate at its provider.

  **Do not restart services yet** — restart once after Task 3's decision so the
  outage (session invalidation) happens once.

- [ ] **Task 3: `VAULT_KEY` — re-encrypt in place (decision made 2026-07-28: Option B, Railway)**

  Decision: re-encrypt in place (single operator, small ciphertext surface; KMS
  migration deferred). The one-off tool is built, reviewed, and committed:
  `backend/internal/infra/vaultrotate` (+ `cmd/tools/vaultrotate`), tests
  green. It covers `api_keys.encrypted_secret`,
  `connections.credentials_encrypted`, `integrations.credentials`, decodes keys
  exactly like the app (`vaultutil.Resolve`), runs per-column transactions,
  and is resume-safe.

  Sequence (maintenance window): scale api+worker to 0 in Railway →
  `DATABASE_URL=… VAULT_KEY_OLD=… VAULT_KEY_NEW=… go run ./cmd/tools/vaultrotate -dry-run`
  → real run → "rotate + verify OK" → set ALL new env vars in Railway
  (incl. new `VAULT_KEY` + `PASETO_KEY`) → redeploy → smoke tests.

  **STOP condition**: never set a new `VAULT_KEY` and restart without the
  re-encryption having run and verified first — existing ciphertext becomes
  undecryptable and live payments break.

- [ ] **Task 4: Deploy + smoke test (30 min)**

  1. Rolling restart api + worker with the new env.
  2. Smoke test, all must pass:
     - Log in fresh (old sessions were invalidated — expected).
     - Run a sandbox test payment end-to-end (proves a stored PSP credential
       still decrypts under the new vault path).
     - `GET /v1/connections` — connection list loads (vault read path).
     - Send a test email (magic link) — proves RESEND key.
     - Trigger the backup script once manually — proves new bucket credentials.
  3. Boot guard sanity: attempting to boot with the old default `PASETO_KEY`
     fails with `insecure production secret keys`.

- [ ] **Task 5: Purge history + force-push (30 min, freeze still active)**

  Execute `SECRET_ROTATION.md` §3–§4 verbatim:

  ```bash
  git clone --mirror git@github.com:Reevit-Platform/backend.git backend-scrub
  cd backend-scrub
  git filter-repo --path .env --invert-paths
  git filter-repo --path backend/.env --invert-paths
  git push --force --all && git push --force --tags
  ```

  Then post the re-clone instruction in the team channel (every collaborator
  must re-clone or hard-reset; old clones can reintroduce the blob), and
  invalidate cached CI checkouts. Lift the merge freeze.

  Expected verification: `git log --all --full-history -- '**/.env'` returns
  nothing on a fresh clone.

- [ ] **Task 6: GitHub-side residual cleanup (15 min)**

  GitHub keeps unreachable commits accessible for a while. Open a GitHub
  support request to purge cached views of the pre-scrub history (or use the
  repo settings purge if available). Treat the keys as compromised regardless —
  Task 2/3 is what protects you; this is hygiene.

- [ ] **Task 7: Post-rotation misuse audit (45 min)**

  The leak window is "commit `37f455a8^` → rotation time". Check for misuse
  during that window (run against the production DB; adjust table names to
  what `db/queries/` defines):

  1. **Session forgery (PASETO)**: sessions created with unusual
     IP/user-agent vs the user's baseline — `sessions` table, filter
     `created_at` in window, group by user, look for first-seen IPs.
  2. **PSP credential access (VAULT)**: `api_key_audit_logs` and connection
     changes in window (`metadata->>'source'` values you don't recognize;
     actors not in the team).
  3. **Outbound email abuse (Resend)**: Resend dashboard sending log for the
     window — volume spikes or unknown recipients.
  4. **Webhook/config tampering**: `webhooks/config` changes and payout/refund
     rows in window that no team member recognizes.
  5. **Provider-side**: Paystack dashboard audit log for the window.

  Record findings in the plan's close-out note; any positive finding escalates
  from "rotation incident" to "breach response".

- [ ] **Task 8: Close-out**

  - [ ] Every key in `/tmp/leaked-keys.txt` is rotated (tick against inventory).
  - [ ] History scrub verified on a fresh clone.
  - [ ] Smoke tests from Task 4 green.
  - [ ] Misuse audit written up (even if "no findings").
  - [ ] Update `plans/README.md` row 008 → DONE.
  - [ ] `.gitignore` gap fix (root/cli/mcp) is tracked in plan 011 — confirm it
        is scheduled so this exact mistake cannot recur elsewhere in the repo.

## STOP conditions

- Any provider rotation fails or is unclear → pause; do NOT scrub history while
  any leaked key is still live (scrubbing without rotating protects nothing).
- `VAULT_KEY` about to be swapped without the migrating/KMS path → stop
  immediately (data-loss-class mistake).
- Test payment fails after deploy → roll back vault env only, investigate,
  do not proceed to Task 5.

## Verification

- `git log --all --full-history -- '**/.env'` → empty (fresh clone).
- Boot with old default `PASETO_KEY` → fails fast (`insecure production secret keys`).
- Sandbox payment + login + magic-link email + manual backup all green post-rotation.
- Inventory file `/tmp/leaked-keys.txt` fully ticked.
