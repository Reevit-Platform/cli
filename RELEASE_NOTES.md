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

Follow the repository's releasing instructions after reviewing the complete
changes since 0.7.1. Tagging publishes GitHub binaries, Homebrew, and npm;
none of those publishing actions are part of this preparation.
