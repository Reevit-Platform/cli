package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The schema strings are a published contract, and a contract nobody can find
// is a contract nobody relies on. This is also the tripwire for adding a new
// one: a `schemaX` constant that reaches a user's stdout without a row in the
// README is a promise made in private.
func TestEverySchemaIsDocumented(t *testing.T) {
	t.Parallel()

	schemas := []string{
		schemaPaymentsList,
		schemaDoctor,
		schemaListenReady,
		schemaListenEvent,
		schemaTrigger,
		schemaLogin,
	}

	for path, must := range map[string][]string{
		filepath.Join("..", "README.md"):        schemas,
		filepath.Join("..", "npm", "README.md"): {"--json", "--quiet", "NDJSON", "schema"},
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		body := string(raw)

		for _, want := range must {
			if !strings.Contains(body, want) {
				t.Errorf("%s never mentions %q", path, want)
			}
		}
	}
}

// Within a v1 schema fields are only ever added. The README says so; this
// checks the strings themselves still carry a version, because a schema
// without one leaves a consumer branching on the CLI's release number.
func TestEverySchemaCarriesAVersion(t *testing.T) {
	t.Parallel()

	for _, schema := range []string{
		schemaPaymentsList, schemaDoctor, schemaListenReady,
		schemaListenEvent, schemaTrigger, schemaLogin,
	} {
		if !strings.HasPrefix(schema, "reevit.cli.") {
			t.Errorf("schema %q is not namespaced", schema)
		}

		parts := strings.Split(schema, ".")

		last := parts[len(parts)-1]
		if len(last) < 2 || last[0] != 'v' {
			t.Errorf("schema %q does not end in a version", schema)
		}
	}
}
