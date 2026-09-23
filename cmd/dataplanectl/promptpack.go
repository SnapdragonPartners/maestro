package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"orchestrator/internal/dataplane/stack"
	"orchestrator/internal/dataplane/store"
	"orchestrator/internal/orchestrator"
)

// The prompt-pack verbs (Phase 3 item 4 design, D9 and D11), and the
// built-in they move organizations to.
//
// The built-in is loaded HERE, at the composition root, from the file
// system the Orchestrator embeds: the root is the one place that knows it
// is a binary carrying a pack, as it is the one place that reads the
// binary's version. Both are refused before the plane is touched -- the
// version by runningHarness, the pack by the loader -- so a build carrying
// a malformed built-in opens no seam.

// loadBuiltin reads the binary's built-in through the one loader.
func loadBuiltin() (store.BuiltinPromptPack, error) {
	builtin, err := orchestrator.LoadBuiltin(orchestrator.BuiltinPack())
	if err != nil {
		return store.BuiltinPromptPack{}, fmt.Errorf("this binary's built-in prompt pack is unusable, so it may not provision one: %w", err)
	}
	return builtin, nil
}

// openOrchestratorSeam opens the plane as the Orchestrator's composition
// root would: the Orchestrator's types, keys, prompt contract and harness.
// The provisioning and prompt-pack verbs act on the Orchestrator's behalf
// -- the selector they seed is a key only its registry declares -- so they
// open its seam rather than the benchmark verbs'.
//
// The built-in is loaded first and the version parsed inside the opener,
// both BEFORE the seam: a verb that cannot describe what it would install
// touches nothing.
func openOrchestratorSeam(ctx context.Context, cfg *stack.Config) (store.Store, *store.BuiltinPromptPack, error) {
	builtin, err := loadBuiltin()
	if err != nil {
		return nil, nil, err
	}
	open, err := orchestratorOpener(cfg)
	if err != nil {
		return nil, nil, err
	}
	seam, err := open(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("open the data plane: %w", err)
	}
	return seam, &builtin, nil
}

// runPromptPack is the prompt-pack command group.
func runPromptPack(ctx context.Context, cfg *stack.Config, what string, opts *runOptions) error {
	if opts.org == "" {
		return fmt.Errorf("prompt-pack %s needs -org <slug>", what)
	}
	seam, builtin, err := openOrchestratorSeam(ctx, cfg)
	if err != nil {
		return err
	}
	defer seam.Close()

	organization, err := seam.GetOrganizationBySlug(ctx, opts.org)
	if err != nil {
		return fmt.Errorf("resolve organization %q: %w", opts.org, err)
	}
	switch what {
	case "show":
		return showPromptPack(ctx, seam, organization)
	case "select-builtin":
		return selectBuiltin(ctx, seam, organization, builtin)
	default:
		return fmt.Errorf("unknown prompt-pack verb %q", what)
	}
}

// showPromptPack prints what the organization's selector resolves to.
func showPromptPack(ctx context.Context, seam store.Store, organization *store.Organization) error {
	selection, err := seam.GetOrganizationPromptPackSelection(ctx, organization.OrganizationID)
	if err != nil {
		return fmt.Errorf("organization %s: %w", organization.Slug, err)
	}
	printSelection(organization, selection)
	return nil
}

// selectBuiltin reads the current selection, then moves it to the running
// binary's built-in conditional on the version it read (design D11). The
// read and the write are two calls on purpose: the version between them is
// the optimistic-concurrency token, and the seam refuses the write if
// another operator moved the selector meanwhile.
func selectBuiltin(ctx context.Context, seam store.Store, organization *store.Organization, builtin *store.BuiltinPromptPack) error {
	current, err := seam.GetOrganizationPromptPackSelection(ctx, organization.OrganizationID)
	if err != nil {
		// A selector that names nothing installed is what this verb
		// repairs: the record exists, and moving it is the remedy.
		var unresolved *store.PromptSelectorUnresolved
		if errors.As(err, &unresolved) {
			fmt.Printf("organization %s: %v\n", organization.Slug, err)
			return selectBuiltinFrom(ctx, seam, organization, builtin, unresolved.Selector.Version)
		}
		return fmt.Errorf("organization %s: %w", organization.Slug, err)
	}
	fmt.Printf("organization %s currently selects %s\n", organization.Slug, describePack(&current.Pack))
	return selectBuiltinFrom(ctx, seam, organization, builtin, current.Selector.Version)
}

func selectBuiltinFrom(ctx context.Context, seam store.Store, organization *store.Organization, builtin *store.BuiltinPromptPack, expectedVersion int) error {
	selected, err := seam.SelectBuiltinPromptPack(ctx, organization.OrganizationID, *builtin, expectedVersion)
	if err != nil {
		return fmt.Errorf("select the built-in prompt pack for organization %s: %w", organization.Slug, err)
	}
	if !selected.Moved {
		fmt.Printf("already selected: %s\n", describePack(&selected.Selection.Pack))
		return nil
	}
	fmt.Printf("selected %s\n", describePack(&selected.Selection.Pack))
	return nil
}

func printSelection(organization *store.Organization, selection *store.PromptPackSelection) {
	fmt.Printf("organization %s selects %s\n  selector record %s at version %d\n",
		organization.Slug, describePack(&selection.Pack), selection.Selector.ID, selection.Selector.Version)
}

// describePack renders a pack the way an operator needs to see it: the
// identity that selects it, what it is called, what it declares, and where
// it came from.
func describePack(pack *store.InstalledPromptPack) string {
	installation := &pack.Installation
	roles := "no roles"
	if len(installation.DeclaredRoles) > 0 {
		roles = "roles " + strings.Join(installation.DeclaredRoles, ",")
	}
	var installer string
	switch installation.Installer.Kind {
	case store.PromptPackInstalledByBuiltin:
		installer = "built-in of maestro " + installation.Installer.BuiltinMaestroVersion
	case store.PromptPackInstalledByUser:
		installer = "installed by user " + installation.Installer.UserID.String()
	default:
		installer = "installer " + string(installation.Installer.Kind)
	}
	return fmt.Sprintf("%s (%q, %d entries, %s, maestro [%s, %s), %s; content %s)",
		pack.Content.Identity(), installation.DisplayName, len(pack.Content.Entries), roles,
		installation.MinMaestroVersion, installation.MaxMaestroVersion, installer, pack.Content.ContentID)
}
