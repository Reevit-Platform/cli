# Reevit init release acceptance

The release workflow runs the idempotent Next.js test and the complete
`TestInitAdapterMatrix` against a fake Reevit API and package-manager
executables. The matrix covers both Next routers, React/Vite, Nuxt, Vue/Vite,
SvelteKit, Svelte/Vite, Express, Go, FastAPI, Flask, Django, generic Python
with uv, Laravel, and generic Composer. It verifies bootstrap authorization,
project-specific server/browser credentials, exact generated files, env
separation, installer selection, API-level checkout/payment verification,
origin configuration, safe entry mounting, rerun idempotency, and manifest
completion.

It also installs the published SDK packages and compiles/imports the generated
TypeScript, Go, Python, and PHP adapters before GoReleaser is allowed to run.

Before tagging, repeat the production-like smoke path:

1. Create a fresh Next.js App Router project with pnpm.
2. Run the release candidate `reevit init` and accept the defaults.
3. Confirm the plan appears before mutation and package progress stays hidden.
4. Run the printed `pnpm dev` command and open `/reevit-demo`.
5. Complete a simulator checkout.
6. Run `reevit listen` and confirm it reports reuse of `.env.local` without
   printing the signing secret.
7. Run `reevit doctor --app-url http://localhost:3000/reevit-demo
   --webhook-url http://localhost:3000/api/webhooks/reevit --strict`.
8. Repeat the smoke path for Next Pages Router, React/Vite, Nuxt, Vue/Vite,
   SvelteKit, Svelte/Vite, Express, Go, FastAPI, Flask, Django, Laravel, and
   generic Python/PHP projects. Confirm each detected installer is correct.
9. Rerun init and require an idempotent result with no secret replacement.
10. Remove one project key locally. Confirm init refuses silent replacement
    and succeeds only with `--rotate-test-keys`.
