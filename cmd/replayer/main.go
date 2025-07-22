package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/proto"
)

const (
	architectAgentType = "architect"
	claudeAgentType    = "claude"
)

// mustFprintf is a helper that ignores fmt.Fprintf errors (for output formatting).
func mustFprintf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// ReplayConfig holds configuration for the replayer.
type ReplayConfig struct {
	LogFile      string
	AgentCtlPath string
	OutputDir    string
	Verbose      bool
	ExitOnFirst  bool
}

// StoryResultPair represents a STORY message and its corresponding RESULT.
type StoryResultPair struct {
	Story  *proto.AgentMsg
	Result *proto.AgentMsg
}

// ComparisonResult represents the result of comparing two RESULT messages.
type ComparisonResult struct {
	StoryID     string
	AgentType   string
	Error       error
	Matched     bool
	Differences []string
}

func main() {
	var config ReplayConfig
	var showHelp bool

	// Parse command line flags.
	flag.StringVar(&config.LogFile, "log", "", "Path to events.jsonl log file")
	flag.StringVar(&config.AgentCtlPath, "agentctl", "./bin/agentctl", "Path to agentctl binary")
	flag.StringVar(&config.OutputDir, "output", "", "Directory to save replay results (default: temp dir)")
	flag.BoolVar(&config.Verbose, "verbose", false, "Enable verbose output")
	flag.BoolVar(&config.ExitOnFirst, "exit-on-first", false, "Exit on first difference found")
	flag.BoolVar(&showHelp, "help", false, "Show help")

	flag.Usage = func() {
		mustFprintf(os.Stderr, "Story Replayer - Offline Regression Testing Tool\n\n")
		mustFprintf(os.Stderr, "Usage:\n")
		mustFprintf(os.Stderr, "  %s -log <events.jsonl> [options]\n\n", os.Args[0])
		mustFprintf(os.Stderr, "Description:\n")
		mustFprintf(os.Stderr, "  Reads historical event logs, replays STORY messages through agents,\n")
		mustFprintf(os.Stderr, "  and compares new RESULT messages to historical ones for regression testing.\n\n")
		mustFprintf(os.Stderr, "Examples:\n")
		mustFprintf(os.Stderr, "  %s -log logs/events-2025-06-10.jsonl\n", os.Args[0])
		mustFprintf(os.Stderr, "  %s -log logs/events.jsonl -verbose -output ./replay-results\n\n", os.Args[0])
		mustFprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if showHelp {
		flag.Usage()
		os.Exit(0)
	}

	// Validate required arguments.
	if config.LogFile == "" {
		mustFprintf(os.Stderr, "Error: -log flag is required\n\n")
		flag.Usage()
		os.Exit(1)
	}

	// Set default output directory.
	if config.OutputDir == "" {
		tmpDir, err := os.MkdirTemp("", "replayer-*")
		if err != nil {
			mustFprintf(os.Stderr, "Error creating temp directory: %v\n", err)
			os.Exit(1)
		}
		config.OutputDir = tmpDir
		defer func() {
			if err := os.RemoveAll(tmpDir); err != nil {
				fmt.Printf("Warning: failed to remove temp directory: %v\n", err)
			}
		}()
	}

	// Run the replayer.
	exitCode, err := runReplayer(config)
	if err != nil {
		mustFprintf(os.Stderr, "Error: %v\n", err)
	}

	// Let defers run, then exit with proper code
	if exitCode != 0 || err != nil {
		defer os.Exit(exitCode)
		return
	}

	defer os.Exit(0)
}

func runReplayer(config ReplayConfig) (int, error) {
	printStartupInfo(config)

	pairs, err := parseEventLog(config.LogFile)
	if err != nil {
		return 1, fmt.Errorf("failed to parse event log: %w", err)
	}

	if len(pairs) == 0 {
		return 1, fmt.Errorf("no TASK/RESULT pairs found in log file")
	}

	if config.Verbose {
		fmt.Printf("📊 Found %d TASK/RESULT pairs to replay\n\n", len(pairs))
	}

	results, differences := replayAllStories(config, pairs)

	err = generateSummaryReport(config, results)
	if err != nil {
		return 1, fmt.Errorf("failed to generate summary report: %w", err)
	}

	return determineFinalResult(differences, len(pairs)), nil
}

func printStartupInfo(config ReplayConfig) {
	if config.Verbose {
		fmt.Printf("🎬 Story Replayer starting...\n")
		fmt.Printf("   Log file: %s\n", config.LogFile)
		fmt.Printf("   AgentCtl: %s\n", config.AgentCtlPath)
		fmt.Printf("   Output: %s\n", config.OutputDir)
		fmt.Printf("\n")
	}
}

func replayAllStories(config ReplayConfig, pairs []StoryResultPair) ([]ComparisonResult, int) {
	results := make([]ComparisonResult, 0, len(pairs))
	differences := 0

	for i, pair := range pairs {
		if config.Verbose {
			fmt.Printf("🔄 Replaying story %d/%d: %s → %s\n", i+1, len(pairs), pair.Story.FromAgent, pair.Story.ToAgent)
		}

		result := replayStory(config, pair, i+1)
		results = append(results, result)

		if !result.Matched {
			differences++
			printDifferences(config, &result)

			if config.ExitOnFirst {
				if config.Verbose {
					fmt.Printf("\n🚨 Exiting on first difference (--exit-on-first specified)\n")
				}
				break
			}
		} else if config.Verbose {
			fmt.Printf("   ✅ Results match\n")
		}

		if config.Verbose {
			fmt.Printf("\n")
		}
	}

	return results, differences
}

func printDifferences(config ReplayConfig, result *ComparisonResult) {
	if config.Verbose {
		fmt.Printf("   ❌ DIFFERENCE DETECTED\n")
		for _, diff := range result.Differences {
			fmt.Printf("      • %s\n", diff)
		}
	}
}

func determineFinalResult(differences, totalTasks int) int {
	if differences > 0 {
		fmt.Printf("🚨 Regression test FAILED: %d/%d tasks showed differences\n", differences, totalTasks)
		return 1
	}

	fmt.Printf("✅ Regression test PASSED: All %d tasks produced consistent results\n", totalTasks)
	return 0
}

func parseEventLog(logFile string) ([]StoryResultPair, error) {
	// Read messages from the event log.
	messages, err := eventlog.ReadMessages(logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read log file: %w", err)
	}

	// Build a map of TASK messages and their corresponding RESULT messages.
	var pairs []StoryResultPair
	storyMap := make(map[string]*proto.AgentMsg)

	for _, msg := range messages {
		switch msg.Type {
		case proto.MsgTypeSTORY:
			// Store STORY messages by their ID.
			storyMap[msg.ID] = msg

		case proto.MsgTypeRESULT:
			// Find the corresponding STORY for this RESULT.
			if msg.ParentMsgID != "" {
				if story, exists := storyMap[msg.ParentMsgID]; exists {
					// Only include pairs where we can replay the agent.
					agentType := determineAgentType(story.ToAgent)
					if agentType == architectAgentType || agentType == claudeAgentType {
						pairs = append(pairs, StoryResultPair{
							Story:  story,
							Result: msg,
						})
					}
				}
			}
		}
	}

	return pairs, nil
}

func determineAgentType(agentID string) string {
	agentID = strings.ToLower(agentID)
	if strings.Contains(agentID, "architect") || strings.Contains(agentID, "openai") || strings.Contains(agentID, "o3") {
		return architectAgentType
	}
	if strings.Contains(agentID, "claude") || strings.Contains(agentID, "coder") || strings.Contains(agentID, "coding") {
		return claudeAgentType
	}
	return "unknown"
}

func replayStory(config ReplayConfig, pair StoryResultPair, storyNum int) ComparisonResult {
	agentType := determineAgentType(pair.Story.ToAgent)

	result := ComparisonResult{
		StoryID:   pair.Story.ID,
		AgentType: agentType,
		Matched:   false,
	}

	// Create input file for the story.
	inputFile := filepath.Join(config.OutputDir, fmt.Sprintf("story_%d_input.json", storyNum))
	storyJSON, err := json.MarshalIndent(pair.Story, "", "  ")
	if err != nil {
		result.Error = fmt.Errorf("failed to marshal story: %w", err)
		return result
	}

	err = os.WriteFile(inputFile, storyJSON, 0644)
	if err != nil {
		result.Error = fmt.Errorf("failed to write input file: %w", err)
		return result
	}

	// Prepare agentctl command.
	var cmd *exec.Cmd
	outputFile := filepath.Join(config.OutputDir, fmt.Sprintf("story_%d_output.json", storyNum))

	if agentType == architectAgentType {
		// For architect, we need to create a story file instead of using JSON input.
		var storyFile string
		storyFile, err = createStoryFileFromStory(config.OutputDir, pair.Story, storyNum)
		if err != nil {
			result.Error = fmt.Errorf("failed to create story file: %w", err)
			return result
		}
		cmd = exec.Command(config.AgentCtlPath, "run", "architect", "--input", storyFile, "--mock", "--output", outputFile)
	} else if agentType == claudeAgentType {
		cmd = exec.Command(config.AgentCtlPath, "run", "claude", "--input", inputFile, "--mock", "--output", outputFile)
	} else {
		result.Error = fmt.Errorf("unknown agent type: %s", agentType)
		return result
	}

	// Execute the command.
	output, err := cmd.CombinedOutput()
	if err != nil {
		result.Error = fmt.Errorf("agentctl failed: %w\nOutput: %s", err, string(output))
		return result
	}

	// Read the new result.
	newResultData, err := os.ReadFile(outputFile)
	if err != nil {
		result.Error = fmt.Errorf("failed to read output file: %w", err)
		return result
	}

	var newResult proto.AgentMsg
	err = json.Unmarshal(newResultData, &newResult)
	if err != nil {
		result.Error = fmt.Errorf("failed to parse new result: %w", err)
		return result
	}

	// Compare the results.
	result.Differences = compareResults(pair.Result, &newResult)
	result.Matched = len(result.Differences) == 0

	return result
}

func createStoryFileFromStory(outputDir string, story *proto.AgentMsg, storyNum int) (string, error) {
	// Extract story content from the story message.
	storyID, _ := story.GetPayload("story_id")
	content, _ := story.GetPayload("content")

	storyContent := fmt.Sprintf("# Story Replay %d\n\n", storyNum)
	if content != nil {
		storyContent += fmt.Sprintf("%s\n", content)
	} else {
		storyContent += "Replayed story from event log\n"
	}

	// Create story file.
	storyFile := filepath.Join(outputDir, fmt.Sprintf("story_%d.md", storyNum))
	if storyID != nil {
		storyFile = filepath.Join(outputDir, fmt.Sprintf("%s.md", storyID))
	}

	err := os.WriteFile(storyFile, []byte(storyContent), 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write story file: %w", err)
	}

	return storyFile, nil
}

func compareResults(original, newResult *proto.AgentMsg) []string {
	var differences []string

	// Compare message types.
	if original.Type != newResult.Type {
		differences = append(differences, fmt.Sprintf("Message type: %s → %s", original.Type, newResult.Type))
	}

	// Compare payload status.
	origStatus, _ := original.GetPayload("status")
	newStatus, _ := newResult.GetPayload("status")
	if origStatus != newStatus {
		differences = append(differences, fmt.Sprintf("Status: %v → %v", origStatus, newStatus))
	}

	// Compare implementation length (not exact content, as it may vary)
	origImpl, origHasImpl := original.GetPayload("implementation")
	newImpl, newHasImpl := newResult.GetPayload("implementation")

	if origHasImpl != newHasImpl {
		differences = append(differences, fmt.Sprintf("Implementation presence: %t → %t", origHasImpl, newHasImpl))
	} else if origHasImpl && newHasImpl {
		origStr, origOk := origImpl.(string)
		newStr, newOk := newImpl.(string)
		if origOk && newOk {
			// Compare length and basic characteristics rather than exact content.
			origLen := len(origStr)
			newLen := len(newStr)
			lenDiff := abs(origLen - newLen)

			// Allow some variance in implementation length.
			if lenDiff > origLen/4 { // More than 25% difference
				differences = append(differences, fmt.Sprintf("Implementation length: %d → %d (variance: %d)", origLen, newLen, lenDiff))
			}

			// Check for presence of key patterns.
			origHasPackage := strings.Contains(origStr, "package ")
			newHasPackage := strings.Contains(newStr, "package ")
			if origHasPackage != newHasPackage {
				differences = append(differences, fmt.Sprintf("Package declaration: %t → %t", origHasPackage, newHasPackage))
			}
		}
	}

	// Compare test results if present.
	origTestResults, origHasTest := original.GetPayload("test_results")
	newTestResults, newHasTest := newResult.GetPayload("test_results")

	if origHasTest != newHasTest {
		differences = append(differences, fmt.Sprintf("Test results presence: %t → %t", origHasTest, newHasTest))
	} else if origHasTest && newHasTest {
		origSuccess := extractTestSuccess(origTestResults)
		newSuccess := extractTestSuccess(newTestResults)
		if origSuccess != newSuccess {
			differences = append(differences, fmt.Sprintf("Test success: %t → %t", origSuccess, newSuccess))
		}
	}

	return differences
}

func extractTestSuccess(testResults interface{}) bool {
	// Handle both map and struct formats for test results.
	if resultsMap, ok := testResults.(map[string]interface{}); ok {
		if success, exists := resultsMap["success"]; exists {
			if successBool, ok := success.(bool); ok {
				return successBool
			}
		}
	}
	// If we can't extract success, assume false.
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func generateSummaryReport(config ReplayConfig, results []ComparisonResult) error {
	reportFile := filepath.Join(config.OutputDir, "regression_report.txt")

	file, err := os.Create(reportFile)
	if err != nil {
		return fmt.Errorf("failed to create report file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			fmt.Printf("Warning: failed to close output file: %v\n", err)
		}
	}()

	writer := bufio.NewWriter(file)
	defer func() {
		if err := writer.Flush(); err != nil {
			fmt.Printf("Warning: failed to flush output: %v\n", err)
		}
	}()

	// Write header.
	mustFprintf(writer, "Story Replayer - Regression Test Report\n")
	mustFprintf(writer, "Generated: %s\n", time.Now().Format(time.RFC3339))
	mustFprintf(writer, "Log file: %s\n", config.LogFile)
	mustFprintf(writer, "Total tasks: %d\n\n", len(results))

	// Summary statistics.
	matched := 0
	failed := 0
	errors := 0

	for i := range results {
		result := &results[i]
		if result.Error != nil {
			errors++
		} else if result.Matched {
			matched++
		} else {
			failed++
		}
	}

	mustFprintf(writer, "Summary:\n")
	mustFprintf(writer, "  ✅ Matched:     %d\n", matched)
	mustFprintf(writer, "  ❌ Different:   %d\n", failed)
	mustFprintf(writer, "  🚨 Errors:      %d\n", errors)
	mustFprintf(writer, "\n")

	// Detailed results.
	mustFprintf(writer, "Detailed Results:\n")
	mustFprintf(writer, "=================\n\n")

	for i := range results {
		result := &results[i]
		mustFprintf(writer, "Story %d: %s (%s)\n", i+1, result.StoryID, result.AgentType)

		if result.Error != nil {
			mustFprintf(writer, "  🚨 ERROR: %v\n", result.Error)
		} else if result.Matched {
			mustFprintf(writer, "  ✅ MATCHED: Results are consistent\n")
		} else {
			mustFprintf(writer, "  ❌ DIFFERENT: Results have changed\n")
			for _, diff := range result.Differences {
				mustFprintf(writer, "     • %s\n", diff)
			}
		}
		mustFprintf(writer, "\n")
	}

	if config.Verbose {
		fmt.Printf("📄 Detailed report saved to: %s\n", reportFile)
	}

	return nil
}
