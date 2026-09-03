# Working on the reevit CLI

## Commands

| Purpose | Command |
|---|---|
| Build, vet, test | `go build ./... && go vet ./... && go test ./... -count=1` |
| Format (must be empty) | `gofmt -l .` |
| Golden output tests | `go test ./cmd -run TestGolden -count=1` |
| Regenerate goldens | `go test ./cmd -run TestGolden -update` |

## Golden output tests

`cmd/golden_test.go` runs the real root command with injected stdio
(`cmd.ExecuteWith`) and snapshots stdout, stderr and the exit code into
`cmd/testdata/golden/<case>.{stdout,stderr}`. Any change to what the CLI prints
must regenerate them with `-update`; read the resulting diff line by line — it
is the review artefact. Never regenerate to make a red test green.

New cases: no `t.Parallel` (cases `os.Chdir`), route every network call at the
case's `httptest` server, and extend `normaliser.apply` for anything else that
varies. `scaffold.Target.Files` is a map, so anything whose order the user can
see must iterate `Target.SortedFiles()` rather than range the map — the
`init-dry-run-full` golden is the lock on that, and it flaps if anyone ranges
the map directly again.

## Output rules

- All printing happens in `cmd/`, through `cmd.OutOrStdout()` and
  `cmd.ErrOrStderr()` — never `os.Stdout`/`os.Stderr`, or the tests cannot
  capture it. `internal/scaffold`, `internal/setup`, `internal/api` never print.
- `cmd.renderError` mirrors how `main.go` prints a failed run; keep them in step.

## Other invariants

- Persist from `config.LoadFile`, request with `config.Load`. `Load` overlays
  `REEVIT_API_KEY`/`REEVIT_API_URL`/`REEVIT_MODE`, so saving its result writes
  a one-off environment value to disk forever.
- Mode comes from the key prefix (`config.ModeFromKey`), not from
  `REEVIT_MODE` — the backend ignores the header for API-key principals.
- `cmd.Execute` cancels the command context on SIGINT/SIGTERM, so any new
  long-running command must select on `cmd.Context().Done()` and return
  `ExitError{Code: 130, Err: context.Canceled}`.
- `cmd/trigger.go`'s magic amounts mirror the backend's
  `adapters/psp/stub/magic.go`. Do not edit one without the other.
- Flags are package globals and pflag remembers `Changed` across runs; tests
  that execute commands must call `resetFlags()` (`cmd/golden_test.go`).
- Releasing: see the "Releasing" section of `README.md`.
