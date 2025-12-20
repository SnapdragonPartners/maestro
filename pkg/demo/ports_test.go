package demo

import (
	"context"
	"net"
	"testing"
	"time"

	"orchestrator/pkg/config"
)

func TestParseHexIP_IPv4(t *testing.T) {
	tests := []struct {
		name     string
		hexStr   string
		expected string
	}{
		{"all interfaces", "00000000", "0.0.0.0"},
		{"localhost", "0100007F", "127.0.0.1"},
		{"specific IP", "0101A8C0", "192.168.1.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := parseHexIP(tt.hexStr)
			if ip.String() != tt.expected {
				t.Errorf("parseHexIP(%q) = %s, want %s", tt.hexStr, ip.String(), tt.expected)
			}
		})
	}
}

func TestParseHexIP_IPv6(t *testing.T) {
	tests := []struct {
		name       string
		hexStr     string
		isLoopback bool
		isZero     bool
	}{
		{"all interfaces", "00000000000000000000000000000000", false, true},
		{"loopback", "00000000000000000000000001000000", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := parseHexIP(tt.hexStr)
			if ip.IsLoopback() != tt.isLoopback {
				t.Errorf("parseHexIP(%q).IsLoopback() = %v, want %v", tt.hexStr, ip.IsLoopback(), tt.isLoopback)
			}
			if ip.IsUnspecified() != tt.isZero {
				t.Errorf("parseHexIP(%q).IsUnspecified() = %v, want %v", tt.hexStr, ip.IsUnspecified(), tt.isZero)
			}
		})
	}
}

func TestParseProcNetTCP(t *testing.T) {
	// Sample /proc/net/tcp output
	procOutput := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0CEA 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12346 1 0000000000000000 100 0 0 10 0
   2: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12347 1 0000000000000000 100 0 0 10 0
   3: 0100007F:1234 0100007F:1F90 01 00000000:00000000 00:00000000 00000000     0        0 12348 1 0000000000000000 100 0 0 10 0`

	pd := &PortDetector{}
	ports := pd.parseProcNetTCP(procOutput)

	// Should find 3 listening ports (line 3 is state 01 = ESTABLISHED, not LISTEN)
	if len(ports) != 3 {
		t.Fatalf("expected 3 ports, got %d", len(ports))
	}

	// Check port 8080 (0x1F90) on 0.0.0.0
	found8080 := false
	for _, p := range ports {
		if p.Port == 8080 {
			found8080 = true
			if p.BindAddress != "0.0.0.0" {
				t.Errorf("port 8080 bind address = %s, want 0.0.0.0", p.BindAddress)
			}
			if !p.Reachable {
				t.Error("port 8080 should be reachable")
			}
		}
	}
	if !found8080 {
		t.Error("expected to find port 8080")
	}

	// Check port 3306 (0x0CEA) on 127.0.0.1
	found3306 := false
	for _, p := range ports {
		if p.Port == 3306 {
			found3306 = true
			if p.BindAddress != "127.0.0.1" {
				t.Errorf("port 3306 bind address = %s, want 127.0.0.1", p.BindAddress)
			}
			if p.Reachable {
				t.Error("port 3306 should NOT be reachable (loopback)")
			}
		}
	}
	if !found3306 {
		t.Error("expected to find port 3306")
	}

	// Check port 80 (0x0050)
	found80 := false
	for _, p := range ports {
		if p.Port == 80 {
			found80 = true
			if !p.Reachable {
				t.Error("port 80 should be reachable")
			}
		}
	}
	if !found80 {
		t.Error("expected to find port 80")
	}
}

func TestSelectMainPort(t *testing.T) {
	tests := []struct {
		name         string
		cfg          *config.DemoConfig
		detected     []config.PortInfo
		exposed      []int
		expectedPort int
	}{
		{
			name: "user selection takes priority",
			cfg:  &config.DemoConfig{SelectedContainerPort: 9000},
			detected: []config.PortInfo{
				{Port: 8080, Reachable: true},
				{Port: 9000, Reachable: true},
			},
			expectedPort: 9000,
		},
		{
			name: "config override second priority",
			cfg:  &config.DemoConfig{ContainerPortOverride: 3000},
			detected: []config.PortInfo{
				{Port: 8080, Reachable: true},
				{Port: 3000, Reachable: true},
			},
			expectedPort: 3000,
		},
		{
			name: "exposed + listening intersection",
			cfg:  nil,
			detected: []config.PortInfo{
				{Port: 9000, Reachable: true},
				{Port: 5000, Reachable: true},
			},
			exposed:      []int{5000, 5001},
			expectedPort: 5000,
		},
		{
			name: "preference order",
			cfg:  nil,
			detected: []config.PortInfo{
				{Port: 9000, Reachable: true},
				{Port: 8080, Reachable: true},
				{Port: 3000, Reachable: true},
			},
			expectedPort: 8080, // 8080 is higher priority than 3000 in PortPreferenceOrder
		},
		{
			name: "lowest numbered fallback",
			cfg:  nil,
			detected: []config.PortInfo{
				{Port: 9999, Reachable: true},
				{Port: 8888, Reachable: true},
				{Port: 7777, Reachable: true},
			},
			expectedPort: 7777,
		},
		{
			name: "skip unreachable ports",
			cfg:  nil,
			detected: []config.PortInfo{
				{Port: 8080, Reachable: false}, // Loopback - skip
				{Port: 9000, Reachable: true},
			},
			expectedPort: 9000,
		},
		{
			name:         "no reachable ports",
			cfg:          nil,
			detected:     []config.PortInfo{{Port: 8080, Reachable: false}},
			expectedPort: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := SelectMainPort(tt.cfg, tt.detected, tt.exposed)
			if port != tt.expectedPort {
				t.Errorf("SelectMainPort() = %d, want %d", port, tt.expectedPort)
			}
		})
	}
}

func TestBuildDiagnostic_NoListeners(t *testing.T) {
	result := BuildDiagnostic(nil, 0, 0, nil)
	if result.ErrorType != DiagnosticNoListeners {
		t.Errorf("ErrorType = %s, want %s", result.ErrorType, DiagnosticNoListeners)
	}
	if result.Success {
		t.Error("expected Success = false")
	}
}

func TestBuildDiagnostic_LoopbackOnly(t *testing.T) {
	ports := []config.PortInfo{
		{Port: 8080, BindAddress: "127.0.0.1", Reachable: false},
	}
	result := BuildDiagnostic(ports, 0, 0, nil)
	if result.ErrorType != DiagnosticLoopbackOnly {
		t.Errorf("ErrorType = %s, want %s", result.ErrorType, DiagnosticLoopbackOnly)
	}
}

func TestBuildDiagnostic_Success(t *testing.T) {
	ports := []config.PortInfo{
		{Port: 8080, BindAddress: "0.0.0.0", Reachable: true},
	}
	result := BuildDiagnostic(ports, 8080, 32847, nil)
	if !result.Success {
		t.Error("expected Success = true")
	}
	if result.ContainerPort != 8080 {
		t.Errorf("ContainerPort = %d, want 8080", result.ContainerPort)
	}
	if result.HostPort != 32847 {
		t.Errorf("HostPort = %d, want 32847", result.HostPort)
	}
}

func TestTCPProbe_LocalhostRefused(t *testing.T) {
	// Try to connect to a port that's definitely not listening
	err := TCPProbe(context.Background(), "127.0.0.1:59999", 100*time.Millisecond)
	if err == nil {
		t.Error("expected error for refused connection")
	}
}

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		ip         string
		isLoopback bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"0.0.0.0", false},
		{"192.168.1.1", false},
		{"::1", true},
		{"::", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip.IsLoopback() != tt.isLoopback {
				t.Errorf("net.ParseIP(%q).IsLoopback() = %v, want %v", tt.ip, ip.IsLoopback(), tt.isLoopback)
			}
		})
	}
}
