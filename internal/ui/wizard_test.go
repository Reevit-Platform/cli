package ui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Reevit-Platform/cli/internal/scaffold"
)

func testTargets() []scaffold.Target {
	return []scaffold.Target{
		{Key: scaffold.TargetWebhook, Label: "Webhook"},
		{Key: scaffold.TargetCheckout, Label: "Checkout"},
		{Key: scaffold.TargetClient, Label: "Server client"},
	}
}

func TestDefaultAnswersAcceptDetectedRecommendation(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var out bytes.Buffer
	got, err := Customize(context.Background(), strings.NewReader("\n"), &out, testTargets(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("selected %d targets, want all recommended", len(got))
	}
}

func TestConfirmApplyCanCancel(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	err := ConfirmApply(context.Background(), strings.NewReader("n\n"), &bytes.Buffer{}, true)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("error = %v, want ErrCancelled", err)
	}
}

func TestResolveExistingSetupCanReplaceGeneratedFiles(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got, err := ResolveExistingSetup(
		context.Background(),
		strings.NewReader("2\n"),
		&bytes.Buffer{},
		true,
		[]string{"components/reevit-checkout-button.tsx"},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != ExistingSetupOverwrite {
		t.Fatalf("action = %q, want %q", got, ExistingSetupOverwrite)
	}
}

func TestResolveExistingSetupDefaultsToKeepingFiles(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got, err := ResolveExistingSetup(
		context.Background(),
		strings.NewReader("\n"),
		&bytes.Buffer{},
		true,
		[]string{"components/reevit-checkout-button.tsx"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != ExistingSetupKeep {
		t.Fatalf("action = %q, want %q", got, ExistingSetupKeep)
	}
}

func TestResolveExistingSetupCanStartFresh(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got, err := ResolveExistingSetup(
		context.Background(),
		strings.NewReader("3\n"),
		&bytes.Buffer{},
		true,
		nil,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != ExistingSetupFresh {
		t.Fatalf("action = %q, want %q", got, ExistingSetupFresh)
	}
}

func TestAccessibleModeFromEnvironment(t *testing.T) {
	t.Setenv("REEVIT_ACCESSIBLE", "1")
	if !Accessible(false) {
		t.Fatal("REEVIT_ACCESSIBLE=1 did not enable accessible mode")
	}
}

func TestPromptOriginKeepsValidatedDefault(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	got, err := PromptOrigin(
		context.Background(), strings.NewReader("\n"), &bytes.Buffer{},
		"http://localhost:3000", true,
		func(value string) error {
			if value != "http://localhost:3000" {
				return errors.New("unexpected origin")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://localhost:3000" {
		t.Fatalf("origin = %q", got)
	}
}
