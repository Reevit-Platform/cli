package scaffold

import (
	"regexp"
	"strings"
	"testing"
)

// pep604Annotation matches an `X | Y` union in a parameter or return
// annotation. Python < 3.10 evaluates annotations when the function is
// defined, so such a union raises TypeError at import time unless the module
// opts into lazy annotations.
var pep604Annotation = regexp.MustCompile(`(:\s*[\w\[\]\.]+\s*\|\s*[\w\[\]\.]+)|(->\s*[\w\[\]\.]+\s*\|)`)

const lazyAnnotations = "from __future__ import annotations"

func needsLazyAnnotations(source string) bool {
	return pep604Annotation.MatchString(source) && !strings.Contains(source, lazyAnnotations)
}

// TestPythonTemplatesUseLazyAnnotations keeps every generated Python file
// importable on Python 3.9 — macOS's system python3 and the oldest version the
// Python SDK supports — even though CI runners only have newer interpreters.
func TestPythonTemplatesUseLazyAnnotations(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		source string
		flag   bool
	}{
		{"union without the future import", "def f(a: str | None): pass", true},
		{"union with the future import", lazyAnnotations + "\ndef f(a: str | None): pass", false},
		{"no union", "def f(a: str): pass", false},
	} {
		if got := needsLazyAnnotations(tc.source); got != tc.flag {
			t.Errorf("control %q: needsLazyAnnotations = %v, want %v", tc.name, got, tc.flag)
		}
	}

	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}

	checked := 0

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "python-") || !strings.HasSuffix(name, ".tmpl") {
			continue
		}

		source, err := templateFS.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}

		checked++

		if needsLazyAnnotations(string(source)) {
			t.Errorf("%s uses X | Y annotations without %q — breaks Python 3.9", name, lazyAnnotations)
		}
	}

	if checked < 5 {
		t.Fatalf("found %d Python templates, want at least 5 — is the template listing broken?", checked)
	}
}
