package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"orchestrator/internal/dataplane/stack"
	"orchestrator/internal/orchestrator"
)

// runDispatch creates and accepts a dispatch for one Story with a DECLARED
// capability set, and prints the execution (Phase 3 item 5 design, D12; DR's
// decision on open question 3). It is Checkpoint 2's manual path and the way
// an operator exercises the boundary before an agent core exists: item 6
// derives the set from the role and pack, and until then the operator
// declares it.
//
// Two seam calls, in the Orchestrator's order: CreateDispatch derives the
// basis and resolves the pack; AcceptDispatch flips the disposition and
// creates the execution with the configuration in its INSERT. The acting
// user is the Orchestrator's operator (-user), never a further flag: the
// execution acts for whoever dispatched it (D6).
//
// -capabilities may be empty, which is a declared EMPTY set -- an execution
// that may request nothing -- and is what an operator exercising admission
// refusal wants. Every identity is validated by the seam against the
// Orchestrator's family registry (orchestrator.Actions, handed to the seam
// as plane.Caller.Actions): an identity no family declares is refused by
// name and the dispatch stays pending.
func runDispatch(ctx context.Context, cfg *stack.Config, opts *runOptions) error {
	switch {
	case opts.org == "":
		return errors.New("dispatch needs -org <slug>")
	case opts.user == "":
		return errors.New("dispatch needs -user <handle>")
	case opts.story == "":
		return errors.New("dispatch needs -story <id>")
	}
	storyID, err := uuid.Parse(opts.story)
	if err != nil {
		return fmt.Errorf("dispatch: -story %q is not a uuid: %w", opts.story, err)
	}
	capabilities := parseCapabilities(opts.capabilities)

	open, err := orchestratorOpener(cfg)
	if err != nil {
		return err
	}
	o, err := orchestrator.Start(ctx, open, orchestrator.Config{OrganizationSlug: opts.org, OperatorHandle: opts.user})
	if err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}
	defer o.Close()

	dispatch, err := o.Dispatch(ctx, storyID)
	if err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}
	execution, err := o.AcceptDispatch(ctx, dispatch.StoryDispatchID, orchestrator.DispatchConfiguration{
		CapabilitySet: capabilities, Headless: opts.headless,
	})
	if err != nil {
		// The dispatch stays pending: the acceptance is the transaction that
		// refused, and nothing about the Story moved.
		return fmt.Errorf("dispatch %s created and left pending; accept: %w", dispatch.StoryDispatchID, err)
	}
	fmt.Printf("dispatched story %s as %s (operator %s)\n", storyID, dispatch.StoryDispatchID, o.Operator().Handle)
	fmt.Printf("execution %s\n", execution.ExecutionID)
	fmt.Printf("  capability_set %s\n", describeCapabilities(execution.CapabilitySet))
	fmt.Printf("  headless       %v\n", execution.Headless)
	fmt.Printf("  acting_user    %s\n", execution.ActingUserID)
	fmt.Printf("  authority      %s\n", execution.AuthorityState)
	return nil
}

// parseCapabilities splits the -capabilities flag: comma-separated family
// identities, blanks dropped. Order and repetition are the seam's to
// canonicalise; a blank entry between commas is dropped here because it is
// a typo, not a declaration, and the seam would otherwise refuse the run.
func parseCapabilities(flag string) []string {
	var capabilities []string
	for _, entry := range strings.Split(flag, ",") {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			capabilities = append(capabilities, trimmed)
		}
	}
	return capabilities
}

func describeCapabilities(capabilities []string) string {
	if len(capabilities) == 0 {
		return "[] (declared empty)"
	}
	return "[" + strings.Join(capabilities, ", ") + "]"
}
