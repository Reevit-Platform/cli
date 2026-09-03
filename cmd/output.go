package cmd

import (
	"encoding/json"
	"io"

	"github.com/spf13/cobra"
)

// Schema strings are the CLI's machine-readable contract. They are versioned
// from the first release so a consumer can branch on the version rather than
// on the CLI's own version number, which moves for unrelated reasons.
//
// The promise: within a `.v1` schema, fields are only ever ADDED. Renaming or
// removing one requires a `.v2` string and a README note.
const (
	schemaPaymentsList = "reevit.cli.payments.list.v1"
	schemaDoctor       = "reevit.cli.doctor.v1"
	schemaListenReady  = "reevit.cli.listen.ready.v1"
	schemaListenEvent  = "reevit.cli.listen.event.v1"
	schemaTrigger      = "reevit.cli.trigger.v1"
	schemaLogin        = "reevit.cli.login.v1"
)

// outputMode reads the two persistent flags that decide what a run prints.
//
// The errors are deliberately dropped: both flags live on the root as
// persistent flags, so every command in the tree inherits them, and a command
// constructed outside that tree (a test probe, say) simply has no opinion —
// which is the zero value already.
func outputMode(cmd *cobra.Command) (jsonOut, quiet bool) {
	jsonOut, _ = cmd.Flags().GetBool("json")
	quiet, _ = cmd.Flags().GetBool("quiet")

	return jsonOut, quiet
}

// emitJSON writes one JSON document to stdout with a trailing newline.
//
// HTML escaping is off because these documents are read by jq and by other
// programs, never embedded in a page: `&` in a URL is unreadable to a
// human and pointless to a machine.
func emitJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetEscapeHTML(false)

	return enc.Encode(v)
}

// noticeStream returns the stream a command's progress and hints go to:
// stderr, or nothing at all under --quiet.
//
// A writer rather than a `msg(cmd, string)` helper on purpose. Half of the
// conversation sites are Fprintf with arguments, several are multi-line blocks
// with blank lines between them, and one (doctor) hands the stream to another
// type entirely. A helper that takes a string has to be remembered at every
// one of those; a writer cannot be forgotten halfway through a block, and it
// makes "which stream is this line on?" the same question as "is it
// suppressible?", which is the property --quiet is actually about.
//
// Errors never come through here. They are rendered by main.go from the error
// the command returns, so --quiet cannot hide a failure. Neither do warnings:
// "could not verify your key", "stream dropped", "that is not a magic amount"
// are caveats about what just happened, not progress, and they keep writing
// straight to stderr. --quiet asks for less narration, not for less truth.
func noticeStream(cmd *cobra.Command) io.Writer {
	if _, quiet := outputMode(cmd); quiet {
		return io.Discard
	}

	return cmd.ErrOrStderr()
}
