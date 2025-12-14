package github_test

import (
	"context"
	"fmt"
	"log"

	"orchestrator/pkg/github"
)

// ExampleClient_GetPRWorkflowStatus demonstrates checking if workflows are passing for a PR.
func ExampleClient_GetPRWorkflowStatus() {
	ctx := context.Background()
	client := github.NewClient("owner", "repo")

	// Get workflow status for PR #123
	status, err := client.GetPRWorkflowStatus(ctx, 123)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("State: %s\n", status.State)
	fmt.Printf("Total runs: %d\n", status.TotalRuns)
	fmt.Printf("Successful: %d\n", status.Successful)
	fmt.Printf("Failed: %d\n", status.Failed)
	fmt.Printf("Pending: %d\n", status.Pending)

	if status.State == github.WorkflowStateSuccess {
		fmt.Println("All workflows passed!")
	} else if status.State == github.WorkflowStateFailure {
		fmt.Printf("Failed workflows: %v\n", status.FailedRuns)
	}
}

// ExampleClient_IsPRWorkflowPassing demonstrates the convenience method for checking workflow status.
func ExampleClient_IsPRWorkflowPassing() {
	ctx := context.Background()
	client := github.NewClient("owner", "repo")

	// Simple boolean check
	passing, err := client.IsPRWorkflowPassing(ctx, 123)
	if err != nil {
		log.Fatal(err)
	}

	if passing {
		fmt.Println("Ready to merge!")
		// Proceed with merge
	} else {
		fmt.Println("Workflows not passing, cannot merge")
	}
}

// ExampleClient_GetWorkflowRunsForPR demonstrates fetching detailed workflow run information.
func ExampleClient_GetWorkflowRunsForPR() {
	ctx := context.Background()
	client := github.NewClient("owner", "repo")

	// Get detailed workflow runs for a PR
	runs, err := client.GetWorkflowRunsForPR(ctx, 123)
	if err != nil {
		log.Fatal(err)
	}

	for _, run := range runs {
		fmt.Printf("Workflow: %s\n", run.Name)
		fmt.Printf("  Status: %s\n", run.Status)
		if run.Status == "completed" {
			fmt.Printf("  Conclusion: %s\n", run.Conclusion)
		}
		fmt.Printf("  URL: %s\n", run.URL)
	}
}
