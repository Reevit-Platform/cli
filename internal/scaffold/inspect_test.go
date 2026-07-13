package scaffold

import "testing"

func TestReadEnvValue(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)
	write(t, dir, ".env.local", "REEVIT_API_KEY=pfk_test_abc.secret\nREEVIT_WEBHOOK_SECRET=\"whsec_quoted\"\nEMPTY=\n")

	project := Detect(dir)

	if got := ReadEnvValue(project, "REEVIT_API_KEY"); got != "pfk_test_abc.secret" {
		t.Errorf("REEVIT_API_KEY = %q", got)
	}

	if got := ReadEnvValue(project, "REEVIT_WEBHOOK_SECRET"); got != "whsec_quoted" {
		t.Errorf("quoted value = %q, want unquoted", got)
	}

	if got := ReadEnvValue(project, "EMPTY"); got != "" {
		t.Errorf("empty value = %q", got)
	}

	if got := ReadEnvValue(project, "MISSING"); got != "" {
		t.Errorf("missing key = %q", got)
	}
}

func TestSDKPackageFor(t *testing.T) {
	// Next with @reevit/node
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16","@reevit/node":"0.9.0"}}`)

	if pkg, ok := SDKPackageFor(Detect(dir)); !ok || pkg != "@reevit/node" {
		t.Errorf("next+node = %q %v", pkg, ok)
	}

	// Next with only @reevit/react (checkout-only setup) still counts
	dir = t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16","@reevit/react":"0.10.1"}}`)

	if pkg, ok := SDKPackageFor(Detect(dir)); !ok || pkg != "@reevit/react" {
		t.Errorf("next+react = %q %v", pkg, ok)
	}

	// Next without any SDK
	dir = t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)

	if _, ok := SDKPackageFor(Detect(dir)); ok {
		t.Error("no SDK must report not installed")
	}

	// Go with the SDK in go.mod
	dir = t.TempDir()
	write(t, dir, "go.mod", "module x\n\nrequire github.com/Reevit-Platform/go-sdk v0.9.0\n")

	if _, ok := SDKPackageFor(Detect(dir)); !ok {
		t.Error("go.mod require must count as installed")
	}
}

func TestWebhookHandlerPaths(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)
	write(t, dir, "tsconfig.json", "{}")
	write(t, dir, "app/api/webhooks/reevit/route.ts", "")

	file, urlPath := WebhookHandler(Detect(dir))
	if file != "app/api/webhooks/reevit/route.ts" || urlPath != "/api/webhooks/reevit" {
		t.Errorf("next handler = %q %q", file, urlPath)
	}

	// No handler
	dir = t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"next":"16"}}`)

	if file, _ := WebhookHandler(Detect(dir)); file != "" {
		t.Errorf("expected no handler, got %q", file)
	}
}
