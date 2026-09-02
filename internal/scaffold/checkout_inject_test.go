package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// injectorFuzzSeeds are the shapes the tag scanner has to survive: degenerate
// tags that used to panic in jsxTagName, TypeScript generics after the return
// that used to be tokenised as tags, and the ordinary pages that must still
// come out with a marker block.
var injectorFuzzSeeds = []string{
	`export default function App() { return <main>Cart</main>; }`,
	`export default function App() { return <main />; }`,
	`export default function App() {
  return (
    <main>
      <h1>Cart</h1>
    </main>
  );
}`,
	`export default function App() { return <></>; }`,
	`export default function App() { return </>; }`,
	`export default function App() { return <//>; }`,
	`export default function App() { return </ >; }`,
	`export default function App() { return <> </ >; }`,
	`export default function App() { return <main>a</ >; }`,
	`export default function App() {
  return <main>Cart</main>;
}

function useThing() {
  const [values, setValues] = useState<Record<string, string>>({});
  return values;
}`,
	`export default function App() {
  const [values, setValues] = useState<Record<string, string>>({});
  return <main>{Object.keys(values).length}</main>;
}`,
	``,
	`export default`,
	`export default () => <main />`,
}

// FuzzInjectReactCheckout asserts the injector's contract on arbitrary input:
// it never panics, and it either refuses or produces exactly one marker block.
func FuzzInjectReactCheckout(f *testing.F) {
	for _, seed := range injectorFuzzSeeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, content string) {
		updated, err := injectReactCheckout(content, "./components/ReevitCheckoutButton")
		if err != nil {
			return
		}

		if got := strings.Count(updated, checkoutPlacementMarker+":start"); got != 1 {
			t.Fatalf("injected %d marker blocks, want exactly 1:\n%s", got, updated)
		}

		if strings.Count(updated, checkoutPlacementMarker+":end") != 1 {
			t.Fatalf("unbalanced marker block:\n%s", updated)
		}
	})
}

// TestInjectReactCheckoutSurvivesDegenerateTags covers the seeds directly, so
// the guarantee holds in a normal `go test` run and not only under -fuzz.
func TestInjectReactCheckoutSurvivesDegenerateTags(t *testing.T) {
	t.Parallel()

	for _, seed := range injectorFuzzSeeds {
		updated, err := injectReactCheckout(seed, "./components/ReevitCheckoutButton")
		if err != nil {
			continue
		}

		if got := strings.Count(updated, checkoutPlacementMarker+":start"); got != 1 {
			t.Fatalf("seed %q injected %d marker blocks:\n%s", seed, got, updated)
		}
	}
}

// TestInjectReactCheckoutIgnoresGenericsAfterTheReturn pins the bounded scan.
// Before it, the scanner ran to end-of-file and read `<Record<string,` and
// `<string,` as tags, desynchronising the caller's depth tracking.
func TestInjectReactCheckoutIgnoresGenericsAfterTheReturn(t *testing.T) {
	t.Parallel()

	source := `export default function App() {
  return (
    <main>
      <h1>Cart</h1>
    </main>
  );
}

function useValues() {
  const [values, setValues] = useState<Record<string, string>>({});
  return values;
}
`

	updated, err := injectReactCheckout(source, "./components/ReevitCheckoutButton")
	if err != nil {
		t.Fatalf("generics after the return must not break the injector: %v", err)
	}

	if !strings.Contains(updated, "<ReevitCheckoutButton amount={5000} />") {
		t.Fatalf("no button injected:\n%s", updated)
	}

	// The helper below the component must come through untouched.
	if !strings.Contains(updated, "useState<Record<string, string>>({})") {
		t.Fatalf("the generic was rewritten:\n%s", updated)
	}
}

// TestJSXTagNameReturnsEmptyForNamelessTags is the unit-level guard: this used
// to index Fields()[0] on an empty slice and panic.
func TestJSXTagNameReturnsEmptyForNamelessTags(t *testing.T) {
	t.Parallel()

	for _, tag := range []string{"</>", "<//>", "</ >", "<>", "<   >", "</\t>"} {
		if got := jsxTagName(tag); got != "" {
			t.Errorf("jsxTagName(%q) = %q, want \"\"", tag, got)
		}
	}

	if got := jsxTagName("<main className=\"x\">"); got != "main" {
		t.Errorf("jsxTagName = %q, want main", got)
	}

	if got := jsxTagName("</main>"); got != "main" {
		t.Errorf("jsxTagName = %q, want main", got)
	}
}

// TestCheckoutPageEditErrorNamesTheEscapeHatch — a refusal has to tell the
// developer how to proceed, not just that it declined.
func TestCheckoutPageEditErrorNamesTheEscapeHatch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeCheckoutTestFile(t, root, "package.json", `{"dependencies":{"react":"19","vite":"7"}}`)
	writeCheckoutTestFile(t, root, "src/App.jsx", "const App = 1;\nexport default App;\n")

	project := Detect(root)
	target := TargetsFor(project)[0]
	target.Checkout = &CheckoutOptions{PagePath: "src/App.jsx"}

	if err := ConfigureCheckoutTarget(project, &target); err != nil {
		t.Fatal(err)
	}

	_, err := Apply(project, []Target{target}, ApplyOptions{})
	if err == nil {
		t.Fatal("a page with no JSX root must be refused, not rewritten")
	}

	for _, want := range []string{"src/App.jsx", "--checkout-page -"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}
}

// TestInjectedPageIsWrittenAtomically — the injected page replaces a file the
// developer already had. applyFileEdit routes it through atomicWriteFile, so a
// failure cannot truncate the original or strand a temp file next to it.
func TestInjectedPageIsWrittenAtomically(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	page := filepath.Join(root, "page.tsx")
	original := "export default function App() { return <main>Cart</main>; }\n"

	if err := os.WriteFile(page, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}

	// A rename onto a directory fails after the temp file exists — the one
	// window in which an in-place os.WriteFile would already have truncated
	// the developer's source.
	target := filepath.Join(root, "occupied")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := atomicWriteFile(target, []byte("replacement"), 0o644); err == nil {
		t.Fatal("expected the rename onto a directory to fail")
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.Contains(entry.Name(), "reevit-") {
			t.Fatalf("failed write left %s behind", entry.Name())
		}
	}

	// The success path replaces the file in one step and keeps its mode.
	if err := atomicWriteFile(page, []byte("injected"), 0o640); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(page)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", info.Mode().Perm())
	}

	raw, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}

	if string(raw) != "injected" {
		t.Errorf("page = %q", raw)
	}
}
