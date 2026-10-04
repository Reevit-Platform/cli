# Next release: 0.7.2

This version is prepared locally and has not been tagged or published.

The generated webhook handlers dispatch production `event` envelopes and
the legacy `type` envelope. Successful and failed payments arrive as
`payment.updated`, with the outcome in `data.status`; handlers no longer
wait for `payment.succeeded` or `payment.failed` event names.

These fixes are already on `main`, together with executable generated-handler
tests. Release 0.7.1 predates them. Preparing this release changes the npm
wrapper version without duplicating the template implementation.

Before tagging `v0.7.2`:

```sh
go build ./...
go vet ./...
go test ./... -count=1
go test ./internal/scaffold -run 'Test.*WebhookTemplateDispatchesOnEvent|TestEveryWebhookTemplateDispatchesOnEvent|TestNoWebhookTemplateNamesAnEventWeDoNotSend' -count=1
REEVIT_EXTERNAL_ADAPTER_TEST=1 go test ./internal/scaffold -run '^TestExternalGenerated' -count=1
```

The external generated-project checks use the current published Reevit SDKs:
React, Vue, and Svelte 0.11.0; core 0.9.1; Node 0.10.2; Go v0.11.0;
Python 0.11.0; and PHP 0.3.0. These are registry releases, rather than the
unpublished SDK improvement candidates being prepared separately.

All generated adapter groups passed against those versions on October 4, 2026:
Next App and Pages Router, React Vite, Express, Go, FastAPI, Flask, Django,
generic Python, PHP, Laravel, Vue Vite, Svelte Vite, Nuxt, and SvelteKit.
These checks compile or import the generated sources and check PHP syntax;
provider payment execution still needs its own sandbox validation.

To retain generated sources and dependency locks for independent review, set
`REEVIT_EXTERNAL_ARTIFACT_DIR` to an empty artifact directory when running the
external checks. Without that variable, the fixtures use temporary directories
that Go removes after the test run.

Follow the repository's releasing instructions after reviewing the complete
changes since 0.7.1. Tagging publishes GitHub binaries, Homebrew, and npm;
none of those publishing actions are part of this preparation.
