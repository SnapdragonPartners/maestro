package main

import (
	"context"
	"errors"
	"fmt"

	"orchestrator/internal/dataplane/stack"
	"orchestrator/internal/dataplane/store"
)

// runProvision is the provisioning command group (Phase 3 item 3, design
// D11): organization, user, product, repository. Each verb is idempotent
// by natural key and reports created versus existing; differing data is a
// typed conflict, never a silent rename.
//
// Feature, Epic and Story creation are deliberately NOT here. Item 11 owns
// the first manual intake surface, and a CLI shaped now would be the thing
// it has to unbuild.
func runProvision(ctx context.Context, cfg *stack.Config, what string, opts *runOptions) error {
	if opts.org == "" {
		return errors.New("provision needs -org <slug>")
	}
	seam, err := openSeam(ctx, cfg)
	if err != nil {
		return err
	}
	defer seam.Close()

	switch what {
	case "organization":
		return provisionOrganization(ctx, seam, opts)
	case "user":
		return provisionUser(ctx, seam, opts)
	case "product":
		return provisionProduct(ctx, seam, opts)
	case "repository":
		return provisionRepository(ctx, seam, opts)
	default:
		return fmt.Errorf("unknown provisioning target %q", what)
	}
}

func provisionOrganization(ctx context.Context, seam store.Store, opts *runOptions) error {
	organization, err := seam.BootstrapOrganization(ctx, store.BootstrapOrganizationInput{
		Slug: opts.org, DisplayName: orDefault(opts.orgName, opts.org),
	})
	if err != nil {
		return fmt.Errorf("provision organization %q: %w", opts.org, err)
	}
	fmt.Printf("%s organization %s (%s)\n", provisioned(organization.Created),
		organization.Record.Slug, organization.Record.DisplayName)
	return nil
}

func provisionUser(ctx context.Context, seam store.Store, opts *runOptions) error {
	if opts.user == "" {
		return errors.New("provision user needs -user <handle>")
	}
	organization, err := seam.GetOrganizationBySlug(ctx, opts.org)
	if err != nil {
		return fmt.Errorf("resolve organization %q: %w", opts.org, err)
	}
	user, err := seam.BootstrapUser(ctx, store.BootstrapUserInput{
		Handle: opts.user, DisplayName: orDefault(opts.userName, opts.user),
		OrganizationID: organization.OrganizationID,
	})
	if err != nil {
		return fmt.Errorf("provision user %q: %w", opts.user, err)
	}
	fmt.Printf("%s user %s (%s)\n", provisioned(user.Created), user.Record.Handle, user.Record.DisplayName)
	return nil
}

// provisionProduct needs the accountable human: products carry user lineage
// (ADR 0022), so -user names who is provisioning.
func provisionProduct(ctx context.Context, seam store.Store, opts *runOptions) error {
	if opts.product == "" || opts.user == "" {
		return errors.New("provision product needs -product <slug> and -user <handle>")
	}
	organization, user, err := resolveActor(ctx, seam, opts)
	if err != nil {
		return err
	}
	product, err := seam.ProvisionProduct(ctx, store.ProvisionProductInput{
		Slug: opts.product, DisplayName: orDefault(opts.productName, opts.product),
		OrganizationID: organization.OrganizationID, UserID: user.UserID,
	})
	if err != nil {
		return fmt.Errorf("provision product %q: %w", opts.product, err)
	}
	fmt.Printf("%s product %s (%s)\n", provisioned(product.Created), product.Record.Slug, product.Record.DisplayName)
	return nil
}

// provisionRepository names its primary Product with -product. The
// membership row is committed with the repository (design D11).
func provisionRepository(ctx context.Context, seam store.Store, opts *runOptions) error {
	if opts.repo == "" || opts.product == "" || opts.user == "" {
		return errors.New("provision repository needs -repo <slug>, -product <primary product slug> and -user <handle>")
	}
	organization, user, err := resolveActor(ctx, seam, opts)
	if err != nil {
		return err
	}
	product, err := seam.GetProductBySlug(ctx, organization.OrganizationID, opts.product)
	if err != nil {
		return fmt.Errorf("resolve primary product %q: %w", opts.product, err)
	}
	repository, err := seam.ProvisionRepository(ctx, store.ProvisionRepositoryInput{
		Slug: opts.repo, DisplayName: orDefault(opts.repoName, opts.repo),
		OrganizationID: organization.OrganizationID, PrimaryProductID: product.ProductID, UserID: user.UserID,
	})
	if err != nil {
		return fmt.Errorf("provision repository %q: %w", opts.repo, err)
	}
	fmt.Printf("%s repository %s (%s), primary product %s\n", provisioned(repository.Created),
		repository.Record.Slug, repository.Record.DisplayName, product.Slug)
	return nil
}

func resolveActor(ctx context.Context, seam store.Store, opts *runOptions) (*store.Organization, *store.User, error) {
	organization, err := seam.GetOrganizationBySlug(ctx, opts.org)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve organization %q: %w", opts.org, err)
	}
	user, err := seam.GetUserByHandle(ctx, organization.OrganizationID, opts.user)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve user %q: %w", opts.user, err)
	}
	return organization, user, nil
}

// orDefault: display data defaults to the key, so the common case needs one
// flag rather than two. It is still supplied EXPLICITLY to the seam.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
