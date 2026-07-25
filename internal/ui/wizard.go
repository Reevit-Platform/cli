package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/huh/v2"

	"github.com/Reevit-Platform/cli/internal/scaffold"
)

var ErrCancelled = errors.New("setup cancelled")

type ExistingSetupAction string

const (
	ExistingSetupKeep      ExistingSetupAction = "keep"
	ExistingSetupOverwrite ExistingSetupAction = "overwrite"
	ExistingSetupFresh     ExistingSetupAction = "fresh"
)

func Accessible(explicit bool) bool {
	return explicit || os.Getenv("REEVIT_ACCESSIBLE") == "1" || os.Getenv("ACCESSIBLE") == "1"
}

// Customize presents the recommended capabilities as selected by default.
// Returning without customization preserves the full adapter recommendation.
func Customize(
	ctx context.Context,
	in io.Reader,
	out io.Writer,
	available []scaffold.Target,
	accessible bool,
) ([]scaffold.Target, error) {
	customize := false
	if err := runForm(ctx, in, out, accessible,
		huh.NewConfirm().
			Title("Customize the recommended setup?").
			Description("The default configures the complete test flow for this project.").
			Negative("Use recommended").
			Value(&customize),
	); err != nil {
		return nil, err
	}
	if !customize {
		return available, nil
	}

	selected := make([]string, 0, len(available))
	options := make([]huh.Option[string], 0, len(available))
	for _, target := range available {
		key := string(target.Key)
		selected = append(selected, key)
		options = append(options, huh.NewOption(target.Label, key).Selected(true))
	}
	field := huh.NewMultiSelect[string]().
		Title("Choose advanced capabilities").
		Description("Space toggles an item; Enter applies the selection.").
		Options(options...).
		Value(&selected).
		Validate(func(values []string) error {
			if len(values) == 0 {
				return fmt.Errorf("select at least one capability")
			}
			return nil
		})
	if err := runForm(ctx, in, out, accessible, field); err != nil {
		return nil, err
	}

	wanted := make(map[string]bool, len(selected))
	for _, value := range selected {
		wanted[value] = true
	}
	picked := make([]scaffold.Target, 0, len(selected))
	for _, target := range available {
		if wanted[string(target.Key)] {
			picked = append(picked, target)
		}
	}
	return picked, nil
}

func ConfirmApply(ctx context.Context, in io.Reader, out io.Writer, accessible bool) error {
	confirmed := true
	err := runForm(ctx, in, out, accessible,
		huh.NewConfirm().
			Title("Apply these changes?").
			Affirmative("Apply setup").
			Negative("Cancel").
			Value(&confirmed),
	)
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrCancelled
	}
	return nil
}

// ResolveExistingSetup lets an interactive user decide how init should handle
// generated output paths that already exist.
func ResolveExistingSetup(
	ctx context.Context,
	in io.Reader,
	out io.Writer,
	accessible bool,
	conflicts []string,
	hasManifest bool,
) (ExistingSetupAction, error) {
	action := ExistingSetupKeep
	options := []huh.Option[ExistingSetupAction]{
		huh.NewOption(
			"Keep existing files",
			ExistingSetupKeep,
		),
		huh.NewOption(
			"Replace generated integration files (creates a backup)",
			ExistingSetupOverwrite,
		),
	}
	if hasManifest {
		options = append(options, huh.NewOption(
			"Start fresh (backup files and rotate test keys)",
			ExistingSetupFresh,
		))
	}

	description := "Reevit found an existing setup."
	if len(conflicts) > 0 {
		description = "Existing output paths:\n" + strings.Join(conflicts, "\n")
	}
	field := huh.NewSelect[ExistingSetupAction]().
		Title("How should Reevit continue?").
		Description(description).
		Options(options...).
		Value(&action)
	if err := runForm(ctx, in, out, accessible, field); err != nil {
		return "", err
	}
	return action, nil
}

// PromptOrigin asks for the local checkout origin and validates it before the
// mutation preview is shown.
func PromptOrigin(
	ctx context.Context,
	in io.Reader,
	out io.Writer,
	defaultOrigin string,
	accessible bool,
	validate func(string) error,
) (string, error) {
	origin := defaultOrigin
	field := huh.NewInput().
		Title("Local app URL").
		Description("Reevit will allow this origin for test checkout.").
		Value(&origin).
		Validate(validate)
	if err := runForm(ctx, in, out, accessible, field); err != nil {
		return "", err
	}
	return origin, nil
}

func runForm(ctx context.Context, in io.Reader, out io.Writer, accessible bool, fields ...huh.Field) error {
	form := huh.NewForm(huh.NewGroup(fields...)).
		WithInput(in).
		WithOutput(out).
		WithAccessible(accessible)
	if err := form.RunWithContext(ctx); err != nil {
		if errors.Is(err, huh.ErrUserAborted) || errors.Is(err, context.Canceled) {
			return ErrCancelled
		}
		return err
	}
	return nil
}
