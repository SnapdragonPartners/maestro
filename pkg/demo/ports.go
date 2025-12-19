package demo

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"orchestrator/pkg/config"
)

// getPortPreferenceOrder returns the priority order for selecting the main container port.
// Lower index = higher priority.
func getPortPreferenceOrder() []int {
	return []int{80, 443, 8080, 8000, 3000, 5000, 5173, 4000}
}

// PortDetector handles detection of listening ports inside containers.
type PortDetector struct {
	commandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
	containerName string
}

// NewPortDetector creates a new port detector for the given container.
func NewPortDetector(containerName string) *PortDetector {
	return &PortDetector{
		containerName: containerName,
	}
}

// SetCommandRunner sets a custom command runner (for testing).
func (pd *PortDetector) SetCommandRunner(runner func(ctx context.Context, name string, args ...string) *exec.Cmd) {
	pd.commandRunner = runner
}

// DetectListeners reads /proc/net/tcp and /proc/net/tcp6 from inside the container
// to find all listening TCP sockets.
func (pd *PortDetector) DetectListeners(ctx context.Context) ([]config.PortInfo, error) {
	// Read both IPv4 and IPv6 TCP listeners
	output, err := pd.execInContainer(ctx, "cat", "/proc/net/tcp", "/proc/net/tcp6")
	if err != nil {
		return nil, fmt.Errorf("failed to read procfs: %w", err)
	}

	return pd.parseProcNetTCP(output), nil
}

// WaitForListeners polls for TCP listeners until at least one is found or timeout.
func (pd *PortDetector) WaitForListeners(ctx context.Context, timeout, pollInterval time.Duration) ([]config.PortInfo, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		ports, err := pd.DetectListeners(ctx)
		if err == nil && len(ports) > 0 {
			return ports, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for listeners: %w", ctx.Err())
		case <-time.After(pollInterval):
			// Continue polling
		}
	}

	return nil, fmt.Errorf("no TCP listeners detected after %v", timeout)
}

// execInContainer runs a command inside the container.
func (pd *PortDetector) execInContainer(ctx context.Context, command string, args ...string) (string, error) {
	dockerArgs := append([]string{"exec", pd.containerName, command}, args...)

	var cmd *exec.Cmd
	if pd.commandRunner != nil {
		cmd = pd.commandRunner(ctx, "docker", dockerArgs...)
	} else {
		cmd = exec.CommandContext(ctx, "docker", dockerArgs...)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker exec failed: %w (output: %s)", err, string(output))
	}

	return string(output), nil
}

// parseProcNetTCP parses the output of /proc/net/tcp and /proc/net/tcp6.
// Format: sl local_address rem_address st tx_queue rx_queue ...
// Example: 0: 00000000:1F90 00000000:0000 0A 00000000:00000000 ...
// State 0A = LISTEN.
func (pd *PortDetector) parseProcNetTCP(output string) []config.PortInfo {
	lines := strings.Split(output, "\n")
	ports := make([]config.PortInfo, 0, len(lines))
	seen := make(map[int]bool) // Dedupe by port number

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "sl") {
			// Skip empty lines and header
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		// Parse local_address (field 1): ADDR:PORT in hex
		localAddr := fields[1]
		addrParts := strings.Split(localAddr, ":")
		if len(addrParts) != 2 {
			continue
		}

		// Parse state (field 3): 0A = LISTEN
		state := fields[3]
		if state != "0A" {
			continue // Not a listening socket
		}

		// Parse port from hex
		portHex := addrParts[1]
		port64, err := strconv.ParseInt(portHex, 16, 32)
		if err != nil {
			continue
		}
		port := int(port64)

		// Skip if already seen
		if seen[port] {
			continue
		}
		seen[port] = true

		// Parse bind address
		bindAddr := parseHexIP(addrParts[0])
		reachable := !bindAddr.IsLoopback()

		ports = append(ports, config.PortInfo{
			Port:        port,
			BindAddress: bindAddr.String(),
			Protocol:    "tcp",
			Exposed:     false, // Will be updated separately if needed
			Reachable:   reachable,
		})
	}

	return ports
}

// parseHexIP converts a hex-encoded IP address from procfs to net.IP.
// IPv4: 8 hex chars (little-endian), e.g., "0100007F" = 127.0.0.1.
// IPv6: 32 hex chars, e.g., "00000000000000000000000001000000" = ::1.
func parseHexIP(hexStr string) net.IP {
	// Remove any leading zeros that might cause issues
	hexStr = strings.TrimSpace(hexStr)

	if len(hexStr) == 8 {
		// IPv4 - little-endian format
		bytes, err := hex.DecodeString(hexStr)
		if err != nil || len(bytes) != 4 {
			return net.IPv4zero
		}
		// Reverse for little-endian
		return net.IPv4(bytes[3], bytes[2], bytes[1], bytes[0])
	} else if len(hexStr) == 32 {
		// IPv6 - stored as 4 little-endian 32-bit words
		bytes, err := hex.DecodeString(hexStr)
		if err != nil || len(bytes) != 16 {
			return net.IPv6zero
		}
		// Reverse each 4-byte group for little-endian
		ip := make(net.IP, 16)
		for i := 0; i < 4; i++ {
			offset := i * 4
			ip[offset] = bytes[offset+3]
			ip[offset+1] = bytes[offset+2]
			ip[offset+2] = bytes[offset+1]
			ip[offset+3] = bytes[offset]
		}
		return ip
	}

	return net.IPv4zero
}

// filterReachablePorts returns only ports that are reachable (not bound to loopback).
func filterReachablePorts(ports []config.PortInfo) []config.PortInfo {
	result := make([]config.PortInfo, 0, len(ports))
	for i := range ports {
		if ports[i].Reachable {
			result = append(result, ports[i])
		}
	}
	return result
}

// findPortInList returns the port number if it exists in the list, or 0 if not found.
func findPortInList(ports []config.PortInfo, targetPort int) int {
	for i := range ports {
		if ports[i].Port == targetPort {
			return ports[i].Port
		}
	}
	return 0
}

// findFirstExposedPort returns the first port that is both in reachable and exposed lists.
func findFirstExposedPort(reachable []config.PortInfo, exposed []int) int {
	exposedSet := make(map[int]bool, len(exposed))
	for _, p := range exposed {
		exposedSet[p] = true
	}
	for i := range reachable {
		if exposedSet[reachable[i].Port] {
			return reachable[i].Port
		}
	}
	return 0
}

// findPreferredPort returns the first port from preference order that exists in reachable.
func findPreferredPort(reachable []config.PortInfo) int {
	reachableSet := make(map[int]bool, len(reachable))
	for i := range reachable {
		reachableSet[reachable[i].Port] = true
	}
	for _, preferred := range getPortPreferenceOrder() {
		if reachableSet[preferred] {
			return preferred
		}
	}
	return 0
}

// findLowestPort returns the lowest numbered port from the list.
func findLowestPort(ports []config.PortInfo) int {
	if len(ports) == 0 {
		return 0
	}
	lowest := ports[0].Port
	for i := 1; i < len(ports); i++ {
		if ports[i].Port < lowest {
			lowest = ports[i].Port
		}
	}
	return lowest
}

// SelectMainPort chooses the "main" container port using the priority order:
// 1. User selection (SelectedContainerPort)
// 2. Config override (ContainerPortOverride)
// 3. EXPOSE + LISTEN intersection
// 4. Preference order intersection
// 5. Lowest numbered listening port.
func SelectMainPort(cfg *config.DemoConfig, detectedPorts []config.PortInfo, exposedPorts []int) int {
	reachable := filterReachablePorts(detectedPorts)
	if len(reachable) == 0 {
		return 0
	}

	// 1. User selection
	if cfg != nil && cfg.SelectedContainerPort > 0 {
		if port := findPortInList(reachable, cfg.SelectedContainerPort); port > 0 {
			return port
		}
	}

	// 2. Config override
	if cfg != nil && cfg.ContainerPortOverride > 0 {
		if port := findPortInList(reachable, cfg.ContainerPortOverride); port > 0 {
			return port
		}
	}

	// 3. EXPOSE + LISTEN intersection
	if port := findFirstExposedPort(reachable, exposedPorts); port > 0 {
		return port
	}

	// 4. Preference order intersection
	if port := findPreferredPort(reachable); port > 0 {
		return port
	}

	// 5. Lowest numbered
	return findLowestPort(reachable)
}

// GetExposedPorts retrieves EXPOSE ports from the Docker image.
func GetExposedPorts(ctx context.Context, imageID string) ([]int, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect",
		"--format", "{{range $p, $_ := .Config.ExposedPorts}}{{$p}} {{end}}",
		imageID)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker inspect failed: %w", err)
	}

	var ports []int
	// Output format: "8080/tcp 3000/tcp "
	parts := strings.Fields(string(output))
	for _, part := range parts {
		// Parse "PORT/PROTO"
		portProto := strings.Split(part, "/")
		if len(portProto) >= 1 {
			port, err := strconv.Atoi(portProto[0])
			if err == nil {
				ports = append(ports, port)
			}
		}
	}

	return ports, nil
}

// TCPProbe attempts to connect to the given address to verify the port is reachable.
func TCPProbe(ctx context.Context, addr string, timeout time.Duration) error {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("tcp probe to %s failed: %w", addr, err)
	}
	_ = conn.Close() // Best effort close, error not critical for probe
	return nil
}

// DiagnosticResult contains information about port detection results for user feedback.
//
//nolint:govet // fieldalignment: Logical grouping preferred for readability
type DiagnosticResult struct {
	Success          bool
	HostPort         int
	ContainerPort    int
	DetectedPorts    []config.PortInfo
	UnreachablePorts []config.PortInfo // Ports bound to loopback
	Error            string
	ErrorType        DiagnosticErrorType
}

// DiagnosticErrorType categorizes the type of error for UI display.
type DiagnosticErrorType string

// Diagnostic error types for categorizing port detection failures.
const (
	DiagnosticNone            DiagnosticErrorType = ""
	DiagnosticContainerExited DiagnosticErrorType = "container_exited"
	DiagnosticNoListeners     DiagnosticErrorType = "no_listeners"
	DiagnosticLoopbackOnly    DiagnosticErrorType = "loopback_only"
	DiagnosticUDPOnly         DiagnosticErrorType = "udp_only"
	DiagnosticProbeFailure    DiagnosticErrorType = "probe_failure"
)

// BuildDiagnostic creates a diagnostic result from detection results.
func BuildDiagnostic(ports []config.PortInfo, selectedPort, hostPort int, probeErr error) DiagnosticResult {
	result := DiagnosticResult{
		DetectedPorts: ports,
		ContainerPort: selectedPort,
		HostPort:      hostPort,
	}

	// Separate reachable and unreachable ports
	for i := range ports {
		if !ports[i].Reachable {
			result.UnreachablePorts = append(result.UnreachablePorts, ports[i])
		}
	}

	// Check for errors
	if len(ports) == 0 {
		result.ErrorType = DiagnosticNoListeners
		result.Error = "Container is running, but no TCP ports are listening."
		return result
	}

	// Check if all ports are loopback-bound
	allLoopback := true
	for i := range ports {
		if ports[i].Reachable {
			allLoopback = false
			break
		}
	}
	if allLoopback {
		result.ErrorType = DiagnosticLoopbackOnly
		result.Error = fmt.Sprintf("App is listening on %s:%d inside the container, so it can't be reached via published ports. It must bind to 0.0.0.0 (or ::).",
			ports[0].BindAddress, ports[0].Port)
		return result
	}

	if probeErr != nil {
		result.ErrorType = DiagnosticProbeFailure
		result.Error = fmt.Sprintf("TCP probe failed: %v", probeErr)
		return result
	}

	result.Success = true
	return result
}
