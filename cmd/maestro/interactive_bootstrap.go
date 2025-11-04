package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/term"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	bootstrapTemplate "orchestrator/pkg/templates/bootstrap"
	"orchestrator/pkg/workspace"
)

// runInteractiveBootstrapSetup handles the complete interactive bootstrap setup process.
// This migrates the full interactive flow from the original handleInit/initializeProject.
func (f *BootstrapFlow) runInteractiveBootstrapSetup(ctx context.Context) ([]byte, error) {
	logger := logx.NewLogger("interactive-bootstrap")

	fmt.Println("🚀 Maestro Bootstrap Setup")
	fmt.Println("This will set up your project with interactive configuration.")
	fmt.Println()

	// Get current working directory
	projectDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	// Step 1: Get git repository
	gitRepo, err := getGitRepository(f.gitRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to get git repository: %w", err)
	}

	// Step 2: Get target branch
	targetBranch := getTargetBranch()

	// Step 3: Update git config with user choices
	gitConfig := &config.GitConfig{
		RepoURL:       gitRepo,
		TargetBranch:  targetBranch,
		MirrorDir:     config.DefaultMirrorDir,
		BranchPattern: config.DefaultBranchPattern,
	}
	if gitErr := config.UpdateGit(gitConfig); gitErr != nil {
		return nil, fmt.Errorf("failed to update git config: %w", gitErr)
	}

	// Step 4: Get the converted URL from config (SSH -> HTTPS conversion)
	currentConfig, configErr := config.GetConfig()
	if configErr != nil {
		return nil, fmt.Errorf("failed to get updated config: %w", configErr)
	}
	convertedGitRepo := currentConfig.Git.RepoURL

	// Step 5: Setup git mirror
	fmt.Println("🔗 Setting up git repository access...")
	if mirrorErr := setupGitMirror(projectDir, convertedGitRepo); mirrorErr != nil {
		return nil, fmt.Errorf("failed to setup git mirror: %w", mirrorErr)
	}

	// Step 6: Detect platform using temporary clone
	fmt.Println("🔍 Detecting platform from repository files...")
	platform, confidence, clonePath, cleanupFn, err := detectPlatformAndCreateWorktree(ctx, projectDir, targetBranch)
	if err != nil {
		return nil, fmt.Errorf("failed to detect platform: %w", err)
	}

	// Ensure cleanup
	if cleanupFn != nil {
		defer cleanupFn()
	}

	// Step 7: Gather user input for customization
	params, _, err := gatherUserInputNew(platform, confidence, projectDir, convertedGitRepo, targetBranch, clonePath)
	if err != nil {
		return nil, fmt.Errorf("failed to gather user input: %w", err)
	}

	// Step 8: Update configuration with user choices
	if updateErr := updateConfigWithUserChoices(&params); updateErr != nil {
		return nil, fmt.Errorf("failed to update configuration: %w", updateErr)
	}

	// Step 8.5: Handle credential storage (optional)
	if credErr := handleCredentialStorage(projectDir); credErr != nil {
		// Non-fatal - warn but continue
		fmt.Printf("⚠️  Failed to store credentials: %v\n", credErr)
		fmt.Println("You can set credentials using environment variables.")
	}

	// Step 9: Verify workspace and get report for spec generation
	report, err := workspace.VerifyWorkspace(ctx, projectDir, workspace.VerifyOptions{
		Logger: logger,
		Fast:   true, // Use fast verification for bootstrap
	})
	if err != nil {
		logger.Warn("Workspace verification failed, will use basic bootstrap: %v", err)
		// Continue with basic bootstrap
	}

	// Step 10: Generate bootstrap specification with target container name
	targetContainerName := params.containerName // This is the maestro-{projectname} or user-specified image
	spec, err := generateBootstrapSpecContent(params.name, params.platform, targetContainerName, convertedGitRepo, params.dockerfilePath, report)
	if err != nil {
		return nil, fmt.Errorf("failed to generate bootstrap spec: %w", err)
	}

	return []byte(spec), nil
}

// getGitRepository handles git repository input with validation.
func getGitRepository(gitRepoFlag string) (string, error) {
	if gitRepoFlag != "" {
		return gitRepoFlag, nil
	}

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("🔗 Git repository URL (required): ")

	for scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			return input, nil
		}
		fmt.Print("Git repository URL is required. Please enter: ")
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	return "", fmt.Errorf("no git repository URL provided")
}

// getTargetBranch prompts user for target branch using config default.
func getTargetBranch() string {
	scanner := bufio.NewScanner(os.Stdin)

	// Use current config as default
	currentConfig, err := config.GetConfig()
	if err != nil {
		// Config not loaded yet - that's fine for init
		currentConfig = config.Config{}
	}
	defaultBranch := "main"
	if currentConfig.Git != nil && currentConfig.Git.TargetBranch != "" {
		defaultBranch = currentConfig.Git.TargetBranch
	}

	fmt.Printf("🌿 Target branch [%s]: ", defaultBranch)
	_ = os.Stdout.Sync() // Ensure prompt is displayed before any background log messages
	targetBranch := defaultBranch
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			targetBranch = input
		}
	}

	return targetBranch
}

// setupGitMirror creates git mirror for the repository.
func setupGitMirror(projectDir, gitRepo string) error {
	mirrorDir := filepath.Join(projectDir, ".mirrors")

	// Create mirrors directory
	if err := os.MkdirAll(mirrorDir, 0755); err != nil {
		return fmt.Errorf("failed to create mirrors directory: %w", err)
	}

	// Extract repository name from URL for mirror directory
	parts := strings.Split(gitRepo, "/")
	repoName := strings.TrimSuffix(parts[len(parts)-1], ".git")
	mirrorPath := filepath.Join(mirrorDir, repoName+".git")

	// Check if mirror already exists
	if _, err := os.Stat(mirrorPath); err == nil {
		fmt.Printf("📂 Git mirror already exists at %s\n", mirrorPath)

		// Update existing mirror
		fmt.Println("🔄 Updating existing git mirror...")
		cmd := exec.Command("git", "remote", "update")
		cmd.Dir = mirrorPath
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to update git mirror: %w", err)
		}
		return nil
	}

	// Clone as bare mirror
	fmt.Printf("📥 Creating git mirror at %s...\n", mirrorPath)
	cmd := exec.Command("git", "clone", "--mirror", gitRepo, mirrorPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create git mirror: %w", err)
	}

	fmt.Println("✅ Git mirror created successfully")
	return nil
}

// detectPlatformAndCreateWorktree detects project platform using a temporary shallow clone.
// This avoids git mirror locking issues that occur when using worktrees.
func detectPlatformAndCreateWorktree(ctx context.Context, projectDir, targetBranch string) (string, float64, string, func(), error) {
	logger := logx.NewLogger("platform-detect")

	// Create temporary shallow clone for platform detection
	mirrorDir := filepath.Join(projectDir, ".mirrors")
	logger.Debug("Using mirrors directory: %s", mirrorDir)

	// Find the mirror directory (assuming single repo for now)
	entries, err := os.ReadDir(mirrorDir)
	if err != nil {
		return "", 0, "", nil, fmt.Errorf("failed to read mirrors directory: %w", err)
	}

	if len(entries) == 0 {
		return "", 0, "", nil, fmt.Errorf("no git mirrors found")
	}

	mirrorPath := filepath.Join(mirrorDir, entries[0].Name())
	logger.Debug("Using mirror path: %s", mirrorPath)

	// Create temporary shallow clone for platform detection in project directory
	// This avoids git mirror locking issues that occur with worktrees
	tempDir := filepath.Join(projectDir, "temp", "platform-detect")
	if mkdirErr := os.MkdirAll(tempDir, 0755); mkdirErr != nil {
		return "", 0, "", nil, fmt.Errorf("failed to create temp directory: %w", mkdirErr)
	}

	logger.Debug("Created temp directory for platform detection: %s", tempDir)
	logger.Debug("Project directory is: %s", projectDir)

	// Simple cleanup function for project-local temp directory
	cleanupFn := func() {
		logger.Debug("Cleaning up temp directory: %s", tempDir)
		_ = os.RemoveAll(tempDir) // Ignore cleanup errors
	}

	// Create shallow clone from mirror (avoids git locking issues)
	logger.Debug("Running git clone command: git clone --depth=1 --no-single-branch %s %s", mirrorPath, tempDir)
	cmd := exec.Command("git", "clone", "--depth=1", "--no-single-branch", mirrorPath, tempDir)
	if cmdErr := cmd.Run(); cmdErr != nil {
		logger.Error("Git clone failed: %v", cmdErr)
		cleanupFn()
		return "", 0, "", nil, fmt.Errorf("failed to create shallow clone: %w", cmdErr)
	}
	logger.Debug("Git clone completed successfully to: %s", tempDir)

	// Checkout the target branch in the clone
	checkoutCmd := exec.Command("git", "checkout", targetBranch)
	checkoutCmd.Dir = tempDir
	if cmdErr := checkoutCmd.Run(); cmdErr != nil {
		// If checkout fails, continue with whatever branch we have
		// Platform detection should still work
		logx.NewLogger("platform-detect").Debug("Failed to checkout %s, continuing with default branch: %v", targetBranch, cmdErr)
	}

	// Detect platform using workspace package
	report, err := workspace.VerifyWorkspace(ctx, tempDir, workspace.VerifyOptions{
		Logger: logger,
		Fast:   true, // Use fast verification for platform detection
	})
	if err != nil {
		// If verification fails, use generic platform
		logger.Debug("Platform detection failed, using generic: %v", err)
		return "generic", 0.5, tempDir, cleanupFn, nil
	}

	// Determine platform and confidence from report
	platform := "generic"
	confidence := 0.5

	if report != nil {
		// Simple heuristics based on bootstrap failures - if no failures, use generic
		for i := range report.BootstrapFailures {
			switch report.BootstrapFailures[i].Component {
			case "go_mod":
				platform = "go"
				confidence = 0.9
			case "requirements_txt", "pyproject_toml":
				platform = "python"
				confidence = 0.9
			case "dockerfile":
				platform = "docker"
				confidence = 0.8
			case "package_json":
				platform = "node"
				confidence = 0.9
			}
		}
	}

	logger.Debug("Returning platform detection results: platform=%s, confidence=%.2f, clonePath=%s", platform, confidence, tempDir)
	return platform, confidence, tempDir, cleanupFn, nil
}

// projectParamsNew holds user input parameters.
type projectParamsNew struct {
	name            string
	platform        string
	containerSource string
	containerName   string
	containerImage  string
	dockerfilePath  string
}

// Container source constants.
const (
	containerSourceDetect     = "detect"
	containerSourceDockerfile = "dockerfile"
	containerSourceImageName  = "image"
)

// gatherUserInputNew collects user preferences for project setup.
//
//nolint:cyclop,unparam // User interaction naturally has many conditional branches; error kept for consistency
func gatherUserInputNew(platform string, confidence float64, projectDir, _, _, worktreePath string) (projectParamsNew, float64, error) {
	scanner := bufio.NewScanner(os.Stdin)

	params := projectParamsNew{
		platform: platform,
	}

	// Project name
	defaultName := filepath.Base(projectDir)
	fmt.Printf("📦 Project name [%s]: ", defaultName)
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			params.name = input
		} else {
			params.name = defaultName
		}
	} else {
		params.name = defaultName
	}

	// Platform confirmation
	fmt.Printf("🖥️  Platform detected as '%s' (%.0f%% confidence). Keep this? [Y/n]: ", platform, confidence*100)
	if scanner.Scan() {
		input := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if input == "n" || input == "no" {
			// Ask for platform
			fmt.Print("Enter platform (go/node/python/docker/generic): ")
			if scanner.Scan() {
				newPlatform := strings.TrimSpace(scanner.Text())
				if newPlatform != "" {
					params.platform = newPlatform
					confidence = 1.0 // User confirmed
				}
			}
		}
	}

	// Container configuration
	fmt.Println("\n🐳 Container Configuration:")
	fmt.Println("1. detect - Let Maestro choose based on platform")
	fmt.Println("2. dockerfile - Use existing Dockerfile")
	fmt.Println("3. image - Specify Docker image name")
	fmt.Print("Choose container source [1]: ")

	containerChoice := "1"
	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			containerChoice = input
		}
	}

	switch containerChoice {
	case "2", "dockerfile":
		params.containerSource = containerSourceDockerfile

		// Look for Dockerfile in worktree
		dockerfilePath := filepath.Join(worktreePath, "Dockerfile")
		if _, err := os.Stat(dockerfilePath); err == nil {
			fmt.Printf("📄 Found Dockerfile at ./Dockerfile. Use it? [Y/n]: ")
			if scanner.Scan() {
				input := strings.ToLower(strings.TrimSpace(scanner.Text()))
				if input != "n" && input != "no" {
					params.dockerfilePath = "Dockerfile" // Store relative path
				}
			}
		}

		if params.dockerfilePath == "" {
			fmt.Print("Enter Dockerfile path: ")
			if scanner.Scan() {
				params.dockerfilePath = strings.TrimSpace(scanner.Text())
			}
		}
		// Use project-specific target name for dockerfile builds
		params.containerName = fmt.Sprintf("maestro-%s", params.name)

	case "3", "image":
		params.containerSource = containerSourceImageName
		fmt.Print("Enter Docker image name: ")
		if scanner.Scan() {
			params.containerImage = strings.TrimSpace(scanner.Text())
		}
		// For user-specified images, use the image name directly
		params.containerName = params.containerImage

	default:
		params.containerSource = containerSourceDetect
		// Set base image for building, but use project-specific target name
		switch params.platform {
		case config.PlatformGo:
			params.containerImage = config.DefaultGoDockerImage
		default:
			params.containerImage = config.DefaultUbuntuDockerImage
		}
		params.containerName = fmt.Sprintf("maestro-%s", params.name)
	}

	return params, confidence, nil
}

// updateConfigWithUserChoices updates the global configuration with user selections.
func updateConfigWithUserChoices(params *projectParamsNew) error {
	// Update project info
	projectInfo := &config.ProjectInfo{
		Name:            params.name,
		PrimaryPlatform: params.platform,
	}
	if err := config.UpdateProject(projectInfo); err != nil {
		return fmt.Errorf("failed to update project info: %w", err)
	}

	// Update container config - always start with bootstrap container for initial setup
	// The coder will update this to the target container once it's built/ready
	containerConfig := &config.ContainerConfig{
		Name: config.BootstrapContainerTag,
	}

	// Store dockerfile path if using dockerfile mode (coder will need this for building target container)
	if params.containerSource == containerSourceDockerfile && params.dockerfilePath != "" {
		containerConfig.Dockerfile = params.dockerfilePath
	}

	if err := config.UpdateContainer(containerConfig); err != nil {
		return fmt.Errorf("failed to update container config: %w", err)
	}

	return nil
}

// handleCredentialStorage prompts user for credential storage and saves if requested.
// This implements the credential storage flow from the secrets management spec.
func handleCredentialStorage(projectDir string) error {
	fmt.Println()
	fmt.Println("🔐 Credential Storage")
	fmt.Println()
	fmt.Println("By default, Maestro reads your credentials for services like GitHub, Anthropic,")
	fmt.Println("and OpenAI from environment variables.")
	fmt.Println()
	fmt.Println("If you don't know what this means or want to store credentials securely in this")
	fmt.Println("project, Maestro can encrypt and save them for you.")
	fmt.Println()
	fmt.Print("Would you like to store credentials in Maestro? [y/N]: ")

	scanner := bufio.NewScanner(os.Stdin)
	var choice string
	if scanner.Scan() {
		choice = strings.ToLower(strings.TrimSpace(scanner.Text()))
	}

	if choice != "y" && choice != "yes" {
		fmt.Println("✅ Using environment variables for credentials")
		return nil
	}

	// User wants to store credentials - collect password
	password, err := promptForPassword()
	if err != nil {
		return fmt.Errorf("failed to get password: %w", err)
	}

	// Collect secrets
	secrets := make(map[string]string)

	// Required: GITHUB_TOKEN
	fmt.Print("Enter GITHUB_TOKEN (required): ")
	if scanner.Scan() {
		githubToken := strings.TrimSpace(scanner.Text())
		if githubToken == "" {
			return fmt.Errorf("GITHUB_TOKEN is required")
		}
		secrets["GITHUB_TOKEN"] = githubToken
	}

	// Optional: ANTHROPIC_API_KEY
	fmt.Print("Enter ANTHROPIC_API_KEY (optional, press Enter to skip): ")
	if scanner.Scan() {
		anthropicKey := strings.TrimSpace(scanner.Text())
		if anthropicKey != "" {
			secrets["ANTHROPIC_API_KEY"] = anthropicKey
		}
	}

	// Optional: OPENAI_API_KEY
	fmt.Print("Enter OPENAI_API_KEY (optional, press Enter to skip): ")
	if scanner.Scan() {
		openaiKey := strings.TrimSpace(scanner.Text())
		if openaiKey != "" {
			secrets["OPENAI_API_KEY"] = openaiKey
		}
	}

	// TODO: Handle SSL cert/key if WebUI SSL is enabled
	// This would require checking config and prompting for cert/key paths

	// Encrypt and save
	fmt.Println()
	fmt.Println("🔐 Encrypting and saving credentials...")
	if err := config.EncryptSecretsFile(projectDir, password, secrets); err != nil {
		return fmt.Errorf("failed to encrypt secrets: %w", err)
	}

	fmt.Println("✅ Credentials saved to .maestro/secrets.json.enc (file permissions: 0600)")
	return nil
}

// promptForPassword prompts user for password with confirmation.
func promptForPassword() (string, error) {
	maxAttempts := 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		fmt.Println()
		fmt.Print("Enter a password for this Maestro project: ")
		password1, err := term.ReadPassword(syscall.Stdin)
		fmt.Println() // New line after password input
		if err != nil {
			return "", fmt.Errorf("failed to read password: %w", err)
		}

		fmt.Print("Confirm password: ")
		password2, err := term.ReadPassword(syscall.Stdin)
		fmt.Println() // New line after password input
		if err != nil {
			return "", fmt.Errorf("failed to read password: %w", err)
		}

		if !bytes.Equal(password1, password2) {
			if attempt < maxAttempts {
				fmt.Println("❌ Passwords do not match. Please try again.")
				continue
			}
			return "", fmt.Errorf("passwords do not match after %d attempts", maxAttempts)
		}

		// Passwords match
		password := string(password1)

		// Clear password bytes from memory
		for i := range password1 {
			password1[i] = 0
		}
		for i := range password2 {
			password2[i] = 0
		}

		// Display password usage info
		fmt.Println()
		fmt.Println("This password will:")
		fmt.Println("  • Encrypt your credentials (GitHub token, API keys)")
		fmt.Println("  • Secure WebUI access (username: maestro)")
		fmt.Println()
		fmt.Println("⚠️  You'll need this password every time you start Maestro.")
		fmt.Println("💡 Or you can store your password in the environment variable MAESTRO_PASSWORD for passwordless startup.")

		return password, nil
	}

	return "", fmt.Errorf("failed to get matching passwords")
}

// generateBootstrapSpecContent generates the bootstrap specification using the template system.
func generateBootstrapSpecContent(projectName, platform, containerImage, gitRepoURL, dockerfilePath string, report *workspace.VerifyReport) (string, error) {
	// Always use the detailed template system for consistent, comprehensive bootstrap specs
	// If no failures exist, we'll pass an empty failures list but still get detailed setup instructions

	var failures []workspace.BootstrapFailure
	if report != nil {
		failures = report.BootstrapFailures
	}

	// Create a minimal report if none exists to ensure template system works
	if report == nil {
		report = &workspace.VerifyReport{
			BootstrapFailures: failures,
		}
	}

	// Use the detailed bootstrap template system (never the basic hardcoded one)
	spec, err := bootstrapTemplate.GenerateBootstrapSpecFromReportEnhanced(projectName, platform, containerImage, gitRepoURL, dockerfilePath, report)
	if err != nil {
		return "", fmt.Errorf("failed to generate bootstrap spec: %w", err)
	}
	return spec, nil
}
