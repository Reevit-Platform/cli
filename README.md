# reevit CLI

Command-line tool for [Reevit](https://reevit.io): set up Reevit in your
project in one command, test payments locally, drive the sandbox simulator,
and inspect your account.

## Install

```bash
# Homebrew (macOS / Linux)
brew install reevit-platform/tap/reevit

# npm
npm install -g @reevit/cli

# Go
go install github.com/Reevit-Platform/cli@latest   # binary: cli → alias as reevit
```

Prebuilt binaries for every platform are on the
[releases page](https://github.com/Reevit-Platform/cli/releases).

## Quickstart: project to test payment

```bash
cd your-project
reevit init
```

`init` opens your browser to log in (no keys to copy), detects your stack,
creates separate least-privilege test credentials for this project, installs
the matching SDKs, wires your env, creates a runnable checkout example, and
verifies the new credentials against the sandbox.

```bash
# run the dev command printed by the wizard (for example, pnpm dev)
# open the demo URL printed by the wizard
reevit doctor
```

The wizard prints the correct dev command and demo URL for the detected
project. Next.js projects normally use `/reevit-demo` on port `3000`;
Vite-based projects normally use `/reevit-demo.html` on port `5173`.

### What `init` sets up

The recommended **Complete test setup** includes every capability supported by
the project:

| Capability | Result |
| --- | --- |
| Checkout | Framework-native checkout component, runnable demo route, origin-restricted browser credential, and a verified sandbox checkout session |
| Webhooks | Framework-native endpoint with raw-body signature verification and a project webhook secret |
| Server API | Server-only Reevit client wired to the scoped project key and organization |
| Test resources | Project credentials, sandbox simulator connection, allowed local origin, and `.reevit/manifest.json` |

**Webhook replay protection.** A captured delivery can be re-sent verbatim and
its signature still verifies, so the generated handlers do two more things
before they act on an event: they reject a `signature_timestamp` older than
5 minutes, and they acknowledge a `delivery_id` they have already seen with a
2xx without re-processing it. The dedupe set is in-process and bounded — it
does not survive a restart and is not shared across instances. Before
production, back it with a persistent store, such as a unique index on
`delivery_id`.

Choose **Customize setup** in the wizard when you only need part of the
integration, or state the goal directly:

```bash
reevit init --goal full       # checkout + webhook + server client
reevit init --goal checkout   # checkout component and runnable demo
reevit init --goal webhook    # signed webhook receiver
reevit init --goal server     # server API client
```

`reevit init --dry-run` previews local files, dependencies, environment
changes, and remote resources without writing anything.

### Supported projects and installers

| Ecosystem | Frameworks | Installers |
| --- | --- | --- |
| React | Next.js App/Pages Routers, React with Vite | npm, pnpm, Yarn, Bun |
| Vue | Nuxt, Vue with Vite | npm, pnpm, Yarn, Bun |
| Svelte | SvelteKit, Svelte with Vite | npm, pnpm, Yarn, Bun |
| Node.js | Express, generic Node | npm, pnpm, Yarn, Bun |
| Go | Go modules, standard HTTP servers | `go get` |
| Python | FastAPI, Flask, Django, generic Python | uv, Poetry, Pipenv, pip |
| PHP | Laravel, generic PHP | Composer |

For JavaScript projects, the `packageManager` declaration wins. Otherwise the
nearest lockfile is used. Reevit never runs multiple installers because a
repository happens to contain stale lockfiles.

## Commands

| Command | What it does |
| --- | --- |
| `reevit login` | Opens the dashboard in your browser to authorize the CLI — a **test-mode** key is created for you and stored locally (0600). `--key -` reads a key from stdin (nothing lands in your shell history); `--no-browser` prints the URL instead of opening it; `--manual` skips pairing and prompts for a key |
| `reevit init` | Detects the framework, router, source layout, and installer; creates project test credentials; configures the simulator/origin; installs SDKs; writes env and runnable integration files; and performs API-level payment/checkout verification. `--yes` accepts the recommendation without prompts; `--goal`/`--target` customize it |
| `reevit doctor [--app-url url] [--webhook-url url] [--strict] [--e2e]` | Verifies the manifest, scoped project credentials, simulator, allowed origin, env, generated files, running checkout route, and signed/tampered webhook behavior. Exits **3** when it finds problems, so CI can tell a failed check from a failed command |
| `reevit payments list [--status s] [--limit n]` | Recent payments in the current mode |
| `reevit trigger <event>` | Fire a test event by creating a **real** sandbox payment through the simulator |
| `reevit listen --forward-to <url>` | Stream live test-mode events to a local endpoint, signed like production |
| `reevit completion <shell>` | Print a completion script for bash, zsh, fish, or PowerShell |

Every command accepts `--no-color`. See [Colour and glyphs](#colour-and-glyphs)
and [Streams and exit codes](#streams-and-exit-codes).

### `login`

`reevit login` starts a pairing session, prints a code, and opens
`dashboard.reevit.io/cli/confirm`. Check the code matches your terminal, pick
your organization, and approve — the CLI receives a freshly minted
**test-mode** API key scoped to what the CLI actually needs:
`payments:read`, `payments:write`, `webhooks:read`, and `webhooks:write`. It
is never granted `api_keys:*` — a paired key cannot mint another key. The key
is delivered exactly once and never shown in the browser.

When you're ready for live traffic, create a live key in Dashboard →
Developers → API keys and hand it to the CLI without putting it in your shell
history:

```bash
printf '%s' "$LIVE_KEY" | reevit login --key -
```

`--manual` prompts for a key instead of pairing, for a machine with no
browser and no way to pipe one in.

### `init`

Detection covers Next.js App and Pages Routers, React/Vite, Nuxt, Vue/Vite,
SvelteKit, Svelte/Vite, Node/Express, Go, PHP/Laravel, and Python
(FastAPI/Flask/Django) — including TypeScript-vs-JS and `src/` layouts. It
uses the `packageManager` declaration first, then one detected lockfile, and
never runs every package manager in a multi-lockfile repository.

- **Webhook handler** — signature-verified receiver using the SDK's verify
  helper (inlined for Go, whose SDK ships no inbound verifier), with native
  FastAPI, Flask, Django, Laravel, Next, Nuxt, and SvelteKit variants. Standard
  Express, Go default-mux, FastAPI, Flask, Django, and Laravel entries are
  mounted automatically with idempotent markers; custom layouts receive an
  exact standalone mounting instruction instead of a risky source rewrite.
- **Checkout flow** — checkout component, routable demo entry, checkout-only
  browser credential, allowed local origin, and API-level session verification.
  The wizard can also place checkout on an existing React/Next, Vue, or Svelte
  page and configure which customer, amount, reference, and metadata fields the
  generated form collects. Page edits use an idempotent `reevit-checkout`
  marker block.
- **Server client** — SDK client wired to `REEVIT_API_KEY` + `REEVIT_ORG_ID`
  with a payment-intent example

Env wiring is stack-aware: server code reads plain `REEVIT_*` vars, and when
you scaffold checkout the browser-exposed key is written under the framework's
own convention — `NEXT_PUBLIC_REEVIT_CHECKOUT_KEY` on Next.js or
`VITE_REEVIT_CHECKOUT_KEY` on Vite-based stacks. The CLI login key remains in
the user's config; it is never copied into the project or browser bundle.

Place code wherever you like with `--webhook-path`, `--checkout-path`, and
`--client-path`. With a webhook target, `--register-webhook https://…` (or the
interactive prompt) registers the production endpoint in your dashboard —
needs a key with `webhooks:write`, which paired keys have.

Checkout placement can also be fully scripted:

```bash
reevit init --goal checkout \
  --checkout-page src/app/cart/page.tsx \
  --checkout-fields price,name,email,phone,reference \
  --checkout-metadata order_id,product_sku
```

`price` is an alias for `amount`. Customer values are exposed to workflows as
`customer_name`, `customer_email`, and `customer_phone`; custom keys become
`metadata_<key>`. Pass `--checkout-page -` to keep the generated component and
runnable demo standalone.

The wizard defaults to the complete recommended setup. “Customize” reveals
advanced capability choices. It resolves the organization attached to an
existing login key so the account is visible before confirmation. Use
`reevit init --yes` for deterministic CI or scripts; piped execution without
an explicit goal/target is rejected instead of hanging.

When the wizard finds an existing Reevit setup or a generated-file collision,
it asks whether to:

- **Keep existing files** and create only what is missing.
- **Replace generated integration files** after backing up every replaced file
  under `.reevit/backups/`.
- **Start fresh** by backing up prior generated files, removing stale generated
  outputs and Reevit-managed mount blocks, regenerating the selected
  integration, and rotating the project's test credentials.

Non-interactive runs remain conservative: use `--overwrite` to replace
generated files or `--fresh` to replace them and rotate test credentials.
Non-empty unmanaged env values are preserved. Reevit updates only its own
marker-delimited blocks in recognized entry files.

`.reevit/manifest.json` makes interrupted runs recoverable. If only a one-time
project secret was lost, use `reevit init --rotate-test-keys`. Installer output
stays quiet on success; use `--verbose` to stream it or `--keep-logs` to retain
successful logs under the gitignored `.reevit/logs/` directory.

### `doctor`

```bash
reevit doctor --webhook-url http://localhost:3000/api/webhooks/reevit
```

Checks the local manifest/files/env, active project credential IDs and exact
scopes, sandbox simulator, and checkout origin. With `--app-url`, it checks
that the generated checkout route is reachable. With `--webhook-url`, it
signs a synthetic `payment.succeeded` with your
`REEVIT_WEBHOOK_SECRET` and POSTs it — your handler must accept it — then
sends the same payload with a tampered signature — your handler must reject
it — and finally replays the event with a correctly signed 20-minute-old
timestamp, which your handler should also reject (accepting it is a warning,
not a failure). `--strict` turns skipped/unreachable runtime checks and
warnings into CI failures.

Add `--e2e` for the strongest check: doctor fires a **real** sandbox payment
through the simulator, waits for the platform-generated event on your
account's stream, and delivers it to `--webhook-url` with the production
envelope and signature — proving the entire pipeline, not just the signature
contract. Test mode only.

### `trigger` events → simulator magic amounts

Triggering doesn't fake events — it creates a real test-mode payment against
the simulator connection (auto-created on first use) with the documented
magic amount, so webhooks/notifications/SSE all come from the production
pipeline and match production schemas by construction.

| Event | Amount | Behavior |
| --- | --- | --- |
| `payment.succeeded` | 4000 | immediate success |
| `payment.failed` | 4001 | hard decline (no failover) |
| `payment.insufficient_funds` | 4002 | decline (no failover) |
| `payment.timeout` | 4003 | transient — exercises routing failover |
| `payment.provider_downtime` | 4004 | provider down — exercises failover |

`trigger` refuses to run in live mode.

### `listen`

```bash
reevit listen --forward-to http://localhost:3000/api/webhooks/reevit
```

Subscribes to your account's live test-mode event stream and POSTs each event
to your endpoint with the production header set and a real signature
(`X-Reevit-Signature: sha256=<hex HMAC-SHA256 of the raw body>`), so your
verification code runs unchanged. The signing secret comes first from
`--signing-secret`, then the project's `REEVIT_WEBHOOK_SECRET`, then platform
webhook configuration; only the final fallback is ephemeral. Project secrets
are reused without being printed. Reconnects with backoff; test mode only.

## Telemetry

The CLI reports one **anonymous** usage event per command run (command name,
CLI version, OS/arch, detected stack and chosen targets for `init`,
success/failure, duration, org id when logged in, and a random per-install
UUID). Never collected: file contents, paths, keys, or hostnames. Events go
to the Reevit API only — no third-party analytics keys ship in the binary.

Opt out any time:

```bash
export REEVIT_TELEMETRY=0   # or the cross-tool convention: DO_NOT_TRACK=1
```

A one-time notice is printed on stderr **before** the first reported command
runs, not after it.

## Configuration

Env vars override the config file: `REEVIT_API_KEY`, `REEVIT_API_URL`
(default `https://api.reevit.io`), `REEVIT_MODE` (`test`|`live`, default
`test`), `REEVIT_CONFIG` (config file path).

Env-supplied values stay ephemeral: they are overlaid onto what `Load` returns
but are never written back to `~/.config/reevit/config.json`. A `REEVIT_API_KEY`
exported for one shell or one CI job does not become permanent on-disk state —
including under `reevit login`, which ignores it. To persist a key, pass it:

```bash
printf '%s' "$KEY" | reevit login --key -
```

`REEVIT_MODE` is not a switch. The backend resolves an API key's mode from the
key itself, so the CLI derives it the same way: `pfk_live_…` is live,
`pfk_test_…` is test. Setting `REEVIT_MODE` to something the key contradicts is
an error rather than a silent mislabel. It still applies to keys that use a
non-standard prefix.

### Colour and glyphs

Output is coloured and uses `✓`/`✗`/`→` when stderr is a terminal whose
locale is UTF-8. Everything degrades on its own, and you can override it:

| Setting | Effect |
| --- | --- |
| `NO_COLOR=1` (any non-empty value) | no colour, anywhere |
| `--no-color` | the same, for one run |
| `TERM=dumb` | no colour and ASCII stand-ins (`ok`, `x`, `!`, `-`, `>`) |
| `LC_ALL`/`LC_CTYPE`/`LANG` without `UTF-8` | ASCII stand-ins, colour unchanged |
| `FORCE_COLOR=1` or `CLICOLOR_FORCE=1` | colour even when the output is a pipe or a file |

Each stream is resolved on its own, so `reevit payments list > out.txt` keeps
the messages on your terminal coloured while the redirected file stays plain.
The payments table colours its status column, but measures every column on the
undecorated text, so the layout is identical with and without colour — strip
the escape sequences from a coloured table and you get the plain one byte for
byte.

Glyphs render best in a monospace font with full Unicode coverage: JetBrains
Mono, SF Mono, Menlo, Cascadia Code, or Fira Code.

### Streams and exit codes

stdout carries data — the payments table, `listen`'s per-event lines, the
`--dry-run` plan, the payment id from `trigger`. Everything else (prompts,
progress, warnings, errors, and the whole of `doctor`'s diagnosis) goes to
stderr, so `reevit doctor > report.txt` still shows you the verdict.

| Exit code | Meaning |
| --- | --- |
| `0` | success |
| `1` | runtime error — the command tried and failed |
| `2` | usage error — an unknown flag or the wrong number of arguments |
| `3` | `reevit doctor` found problems (the command itself worked) |
| `130` | cancelled with Ctrl-C |

## Credential handling

- The config file is written `0600`, in a `0700` directory.
- Env files that `init` writes (`.env`, `.env.local`) are `0600` — and an
  existing framework-created `0644` file is tightened on append. `.env.example`
  and `.gitignore` stay `0644`: one holds only placeholders, the other is
  committed.
- `init` **refuses** to write a `pfk_live_*` key into a browser-exposed var
  (`NEXT_PUBLIC_*`, `VITE_*`). Bundlers inline those into the public JS bundle,
  so a live secret there is served to every visitor. Use a test-mode key for
  the client bundle.
- The npm wrapper verifies the downloaded release archive against the
  `checksums.txt` GoReleaser publishes, before writing anything executable.

## Releasing

Tag `v*` → GitHub Actions runs GoReleaser: builds all platforms, publishes the
GitHub release, and updates the Homebrew tap (needs the `HOMEBREW_TAP_TOKEN`
repo secret — a fine-grained PAT with `contents:write` on
`Reevit-Platform/homebrew-tap`). The npm wrapper (`npm/`) publishes from the
same workflow via npm trusted publishing (configure the repo + `release.yml`
as a trusted publisher on the `@reevit/cli` package settings); bump
`npm/package.json` to the tag version before tagging.

Checklist:

1. Bump `npm/package.json` to the next semver. The release workflow's version
   guard fails the build if it disagrees with the tag, so this comes first.
2. Update the docs and release notes for anything user-visible.
3. Commit.
4. `git tag vX.Y.Z && git push origin vX.Y.Z` — push the one tag, not
   `--tags`. A bare `--tags` pushes every local tag, including any that a
   sibling checkout left behind.
5. Watch the Actions run: binaries, GitHub release, Homebrew tap, npm wrapper.
6. Confirm the npm publish carried provenance attestations
   (`npm view @reevit/cli dist-tags` then check the package page) — a missing
   attestation means the release did not come from CI.
