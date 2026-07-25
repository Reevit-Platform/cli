package setup

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Reevit-Platform/cli/internal/scaffold"
)

// Goal describes the developer-facing setup outcome.
type Goal string

const (
	GoalAuto     Goal = "auto"
	GoalFull     Goal = "full"
	GoalCheckout Goal = "checkout"
	GoalWebhook  Goal = "webhook"
	GoalServer   Goal = "server"
)

// OperationKind groups one printable, secret-free setup mutation.
type OperationKind string

const (
	InstallPackage    OperationKind = "install_package"
	WriteFile         OperationKind = "write_file"
	WriteEnv          OperationKind = "write_env"
	BootstrapPlatform OperationKind = "bootstrap_platform"
	VerifyIntegration OperationKind = "verify_integration"
)

// Operation is one mutation shown to the developer before apply.
type Operation struct {
	Kind   OperationKind
	Detail string
	Reason string
}

// ResolveInput contains only discovery and developer choices. It never
// carries raw credentials, making the resulting preview safe to print.
type ResolveInput struct {
	Project     scaffold.Project
	Goal        Goal
	Targets     []scaffold.Target
	LocalOrigin string
	Manifest    scaffold.Manifest
}

// RecommendedTargets returns the complete adapter recommendation.
func RecommendedTargets(project scaffold.Project) []scaffold.Target {
	return scaffold.TargetsFor(project)
}

// Resolve creates an immutable, printable setup plan.
func Resolve(input ResolveInput) (Plan, error) {
	if input.Project.Stack == scaffold.StackUnknown {
		return Plan{}, fmt.Errorf("cannot resolve setup for an unknown project")
	}
	if !slices.Contains(
		[]Goal{GoalAuto, GoalFull, GoalCheckout, GoalWebhook, GoalServer},
		input.Goal,
	) {
		return Plan{}, fmt.Errorf("invalid setup goal %q", input.Goal)
	}
	if len(input.Targets) == 0 {
		return Plan{}, fmt.Errorf("setup plan has no selected capabilities")
	}

	conflicts, err := scaffold.ConflictPaths(input.Project, input.Targets, input.Manifest)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{
		Project: input.Project, Targets: slices.Clone(input.Targets),
		Manifest: input.Manifest, Goal: input.Goal,
		LocalOrigin: strings.TrimRight(strings.TrimSpace(input.LocalOrigin), "/"),
		Conflicts:   conflicts,
	}
	if len(input.Project.Managers) > 1 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"multiple lockfiles found; %s is the only package manager that will run",
			input.Project.Manager,
		))
	}

	runCommands, shownCommands := installCommands(input.Project, input.Targets)
	for _, command := range append(runCommands, shownCommands...) {
		plan.Operations = append(plan.Operations, Operation{
			Kind: InstallPackage, Detail: strings.Join(command, " "),
			Reason: "install the SDK selected for this project",
		})
	}
	plan.Operations = append(plan.Operations,
		Operation{
			Kind: BootstrapPlatform, Detail: "create or reuse scoped project test credentials",
			Reason: "keep the account login credential out of application code",
		},
		Operation{
			Kind: BootstrapPlatform, Detail: "ensure the sandbox simulator is ready",
			Reason: "make the generated integration testable immediately",
		},
	)
	if hasTarget(input.Targets, scaffold.TargetCheckout) {
		plan.Operations = append(plan.Operations, Operation{
			Kind:   BootstrapPlatform,
			Detail: "allow checkout origin " + plan.LocalOrigin,
			Reason: "permit the local browser demo to initialize checkout",
		})
	}
	plan.Operations = append(plan.Operations, Operation{
		Kind: WriteEnv, Detail: scaffold.EnvFileName(input.Project) + " and .env.example",
		Reason: "wire separate server, browser, organization, and webhook values",
	})
	for _, target := range input.Targets {
		for _, path := range target.Files {
			plan.Operations = append(plan.Operations, Operation{
				Kind: WriteFile, Detail: filepath.ToSlash(path),
				Reason: target.Label,
			})
		}
		for _, edit := range target.Edits {
			plan.Operations = append(plan.Operations, Operation{
				Kind: WriteFile, Detail: filepath.ToSlash(edit.Path),
				Reason: edit.Description,
			})
		}
	}
	verifyDetail := "verify the generated project credentials against the sandbox"
	if hasTarget(input.Targets, scaffold.TargetCheckout) &&
		!hasTarget(input.Targets, scaffold.TargetClient) {
		verifyDetail = "verify checkout can create a usable sandbox session"
	}
	plan.Operations = append(plan.Operations, Operation{
		Kind: VerifyIntegration, Detail: verifyDetail,
		Reason: "do not report success until the platform accepts the new credentials",
	})

	return plan, nil
}
