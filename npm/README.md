# Reevit CLI

Set up Reevit in an existing project, create isolated test credentials, generate
a working checkout and webhook integration, and test the complete payment
flow—without copying keys between dashboards or reading setup documentation
first.

## Start testing in a few minutes

Install the CLI:

```sh
npm install -g @reevit/cli
```

The npm wrapper requires Node.js 18 or newer. Its postinstall downloads the
matching CLI binary from GitHub Releases for macOS, Linux, or Windows on x64
and arm64.

Then run it inside the application you want to connect:

```sh
cd your-project
reevit init
```

The wizard opens Reevit in your browser so you can sign in, confirm the pairing
code, and choose an organization. It then:

1. Detects your framework, router, source layout, language, and package manager.
2. Creates separate least-privilege **test-mode** credentials for this project.
3. Creates or reuses a sandbox simulator connection.
4. Installs the matching Reevit SDK with the package manager your project uses.
5. Writes the correct environment variables without replacing existing values.
6. Generates a runnable checkout, signature-verifying webhook handler, and
   server client when the project supports them.
7. Verifies the credentials, sandbox checkout session, and simulator connection.
8. Prints the exact command and local URL to try next.

For the recommended complete setup without additional setup-choice prompts
(browser authorization is still required when you are not logged in):

```sh
reevit init --yes
```

Start your application with the command printed by the wizard, open the generated
demo route, and run:

```sh
reevit doctor
```

## What the wizard configures

The default **Complete test setup** includes every safe capability available for
the detected stack:

| Capability | What Reevit creates |
| --- | --- |
| Checkout | A framework-native checkout component, a runnable demo route, an origin-restricted browser key, and a verified sandbox checkout session |
| Webhooks | A framework-native endpoint that verifies `X-Reevit-Signature` against the raw request body |
| Server API | A server-only Reevit client configured with the project API key and organization ID |
| Test environment | Scoped server and checkout credentials, a webhook secret, simulator connection, local origin, and stack-aware env variables |
| Recovery | `.reevit/manifest.json`, interactive conflict resolution, recoverable file backups, idempotent markers, and optional retained setup logs |

Choose **Customize setup** in the wizard or use a goal directly:

```sh
reevit init --goal full
reevit init --goal checkout
reevit init --goal webhook
reevit init --goal server
```

For scripts and CI, choose exact targets:

```sh
reevit init --target webhook --target client --yes
```

Preview every file, dependency, environment change, and remote resource without
writing anything:

```sh
reevit init --dry-run
```

## Supported projects

| Ecosystem | Detected projects | Installer selection |
| --- | --- | --- |
| React | Next.js App Router, Next.js Pages Router, React with Vite | `npm`, `pnpm`, `yarn`, or `bun` |
| Vue | Nuxt and Vue with Vite | `npm`, `pnpm`, `yarn`, or `bun` |
| Svelte | SvelteKit and Svelte with Vite | `npm`, `pnpm`, `yarn`, or `bun` |
| Node.js | Express and generic Node projects | `npm`, `pnpm`, `yarn`, or `bun` |
| Go | Go modules and standard HTTP servers | `go get` |
| Python | FastAPI, Flask, Django, and generic Python projects | `uv`, Poetry, Pipenv, or `pip` |
| PHP | Laravel and generic Composer projects | Composer |

JavaScript package-manager selection uses the `packageManager` field first and
then the nearest lockfile. If a repository contains several lockfiles, Reevit
selects one installer and explains the decision instead of running them all.

The generated code follows the detected TypeScript/JavaScript choice, `src/`
layout, router convention, and client-side env prefix:

- Next.js: `NEXT_PUBLIC_REEVIT_CHECKOUT_KEY`
- Vite-based projects: `VITE_REEVIT_CHECKOUT_KEY`
- Server code: `REEVIT_API_KEY`, `REEVIT_ORG_ID`, and
  `REEVIT_WEBHOOK_SECRET`

The CLI login key stays in your user config. It is never copied into the project
or exposed to the browser.

## Test the integration

### Check the generated checkout

Run the dev command printed by `init`, then pass the demo URL to doctor:

```sh
reevit doctor --app-url http://localhost:3000/reevit-demo
```

Vite-based projects normally use port `5173`; the wizard prints the detected URL.

### Check webhook signatures

Start your app and point doctor at the generated webhook endpoint:

```sh
reevit doctor \
  --webhook-url http://localhost:3000/api/webhooks/reevit
```

Doctor sends one correctly signed request that the endpoint must accept and one
tampered request that it must reject.

For the strongest test, drive a real sandbox payment through Reevit and deliver
the platform-generated event to the endpoint:

```sh
reevit doctor \
  --e2e \
  --strict \
  --webhook-url http://localhost:3000/api/webhooks/reevit
```

### Trigger payment outcomes

`trigger` does not fabricate webhook payloads. It creates a real test-mode
payment with a simulator amount that produces the requested outcome:

```sh
reevit trigger payment.succeeded
reevit trigger payment.failed
reevit trigger payment.insufficient_funds
reevit trigger payment.timeout
reevit trigger payment.provider_downtime
```

### Forward live test events locally

```sh
reevit listen \
  --forward-to http://localhost:3000/api/webhooks/reevit
```

Events are forwarded with the same envelope and signature header used by
production webhooks.

## Safe to rerun

`reevit init` is designed for existing projects:

- The interactive wizard detects existing setup and generated-file collisions.
- **Keep existing files** preserves them and creates only missing outputs.
- **Replace generated integration files** backs up each replaced file under
  `.reevit/backups/` before writing.
- **Start fresh** backs up and regenerates integration files, then rotates the
  project's test credentials.
- Reevit updates only its marker-delimited blocks in recognized entry files.
- Non-empty unmanaged environment values remain untouched.
- Interrupted setup can resume from `.reevit/manifest.json`.
- Successful reruns reuse the project and credentials.
- Lost one-time project secrets and managed env values can be replaced explicitly with
  `reevit init --rotate-test-keys`.
- Installer output stays quiet on success; use `--verbose` to stream it or
  `--keep-logs` to retain successful logs.

For scripts and CI, make replacement behavior explicit:

```sh
reevit init --yes --overwrite # backup and replace generated files
reevit init --yes --fresh     # also rotate project test credentials
```

Use custom paths when your project has its own layout:

```sh
reevit init \
  --webhook-path src/http/reevit-webhook.ts \
  --checkout-path src/components/checkout.tsx \
  --client-path src/lib/reevit.ts
```

## Commands

| Command | Purpose |
| --- | --- |
| `reevit login` | Authorize the CLI through browser pairing and receive a scoped test-mode key |
| `reevit init` | Detect, provision, install, configure, generate, and verify sandbox access |
| `reevit doctor` | Check credentials, env wiring, SDKs, generated files, checkout routes, and webhook behavior |
| `reevit payments list` | Inspect recent payments in the current mode |
| `reevit trigger <event>` | Create a real simulator payment for a test outcome |
| `reevit listen --forward-to <url>` | Stream and sign test events for a local endpoint |

Run `reevit <command> --help` for every option.

## Other installation options

### Homebrew

```sh
brew install reevit-platform/tap/reevit
```

### Prebuilt binaries

Download macOS, Linux, and Windows archives from
[GitHub Releases](https://github.com/Reevit-Platform/cli/releases).

### Go

```sh
go install github.com/Reevit-Platform/cli@latest
```

The Go toolchain installs the binary as `cli`; rename or alias it to `reevit`.

## Configuration and privacy

Environment variables override the user config:

| Variable | Purpose |
| --- | --- |
| `REEVIT_API_KEY` | Use an explicit API key |
| `REEVIT_API_URL` | Override the API URL; defaults to `https://api.reevit.io` |
| `REEVIT_MODE` | `test` or `live`; defaults to `test` |
| `REEVIT_CONFIG` | Override the user config path |
| `REEVIT_TELEMETRY=0` | Disable anonymous CLI usage telemetry |
| `DO_NOT_TRACK=1` | Disable telemetry using the cross-tool convention |

Telemetry never includes file contents, project paths, API keys, secrets, or
hostnames. Run `reevit --help` or read the
[complete CLI guide](https://github.com/Reevit-Platform/cli#readme) for details.

## Troubleshooting

- **The CLI detected the wrong installer:** set the JavaScript
  `packageManager` field or remove stale lockfiles, then rerun `reevit init`.
- **A file already exists:** move it, choose a custom output path, or integrate
  the generated standalone file manually. Reevit will not replace it.
- **The browser cannot open:** use `reevit login --no-browser` and open the
  printed pairing URL yourself.
- **A project secret was lost:** run `reevit init --rotate-test-keys`.
- **The application is not running:** start it first, then pass `--app-url` or
  `--webhook-url` to `reevit doctor`.
- **Need CI-safe verification:** run `reevit doctor --strict`; skipped runtime
  checks become failures.

For bugs and feature requests, open an issue in the
[CLI repository](https://github.com/Reevit-Platform/cli/issues).
