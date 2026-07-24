package scaffold

import "testing"

func TestManifestRoundTripContainsIDsButNoSecrets(t *testing.T) {
	t.Parallel()

	project := Project{Root: t.TempDir(), Stack: StackNext}
	want := Manifest{
		Status: "complete", ProjectID: "rvproj_123", Adapter: "nextjs",
		ServerKeyID: "pfk_test_server", CheckoutKeyID: "pfk_test_checkout",
		Origin: "http://localhost:3000",
	}
	if err := WriteManifest(project, want); err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}
	got, err := ReadManifest(project)
	if err != nil {
		t.Fatalf("ReadManifest() error = %v", err)
	}
	if got != want {
		t.Fatalf("manifest = %#v, want %#v", got, want)
	}
	raw := readFile(t, project.Root, ".reevit/manifest.json")
	if containsSecret(raw) {
		t.Fatalf("manifest contains a raw secret: %s", raw)
	}
}

func containsSecret(raw string) bool {
	return len(raw) > 0 && (contains(raw, ".secret") || contains(raw, "whsec_"))
}

func contains(raw, value string) bool {
	for i := 0; i+len(value) <= len(raw); i++ {
		if raw[i:i+len(value)] == value {
			return true
		}
	}
	return false
}
