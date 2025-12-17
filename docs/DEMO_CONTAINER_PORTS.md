# Demo Container Port Detection

This document describes how Maestro's demo mode detects and exposes container ports.

## Problem Statement

Running user applications in Docker containers requires mapping container ports to host ports. This presents several challenges:

1. **Unknown container ports**: Dev containers may or may not have `EXPOSE` directives, and even when present, they may not reflect what the app actually listens on
2. **Framework diversity**: Apps use many different default ports (3000, 5000, 8000, 8080, etc.)
3. **Host port conflicts**: Fixed host ports can conflict with other services (including Maestro's WebUI on 8080)
4. **Common Docker gotcha**: Apps binding to `127.0.0.1` inside the container are unreachable via port publishing

## Design Overview

Instead of guessing ports, Maestro uses **discovery mode** to observe what the container is actually doing:

```
┌─────────────────────────────────────────────────────────────────┐
│                         FIRST RUN                                │
├─────────────────────────────────────────────────────────────────┤
│  1. Start container (no -p)                                      │
│  2. Run make build && make run                                   │
│  3. Poll /proc/net/tcp* for listeners                           │
│  4. Select "main" port (preference order + EXPOSE intersection) │
│  5. Save detected ports to config                                │
│  6. Restart with -p 127.0.0.1::${containerPort}                 │
│  7. TCP probe to verify                                          │
│  8. Report success or diagnostic                                 │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                      SUBSEQUENT RUNS                             │
├─────────────────────────────────────────────────────────────────┤
│  1. Read cached port from config                                 │
│  2. Start with -p 127.0.0.1::${containerPort}                   │
│  3. Run make build && make run                                   │
│  4. TCP probe to verify                                          │
│  5. If probe fails → fall back to discovery mode                │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                          REBUILD                                 │
├─────────────────────────────────────────────────────────────────┤
│  Default: Re-run discovery (code may have changed ports)        │
│  Option: "Skip port detection" checkbox uses cached config      │
└─────────────────────────────────────────────────────────────────┘
```

## Port Detection Algorithm

### Step 1: Start in Discovery Mode

Start the container **without** `-p` flag initially:

```bash
docker run -d --name maestro-demo \
  --network demo-network \
  --workdir /workspace \
  --volume /path/to/workspace:/workspace \
  ${IMAGE} \
  sh -c "make build && make run"
```

### Step 2: Detect Listeners via procfs

After container starts, poll for TCP listeners by reading Linux procfs directly (works regardless of what tools are installed in the container):

```bash
docker exec maestro-demo cat /proc/net/tcp /proc/net/tcp6 2>/dev/null
```

Parse the output to find LISTEN sockets (state `0A`):

```
  sl  local_address rem_address   st tx_queue rx_queue ...
   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 ...
      ^^^^^^^^ ^^^^              ^^
      bind_addr port            state (0A = LISTEN)
```

Fields to extract:
- **Local address**: Hex IP (e.g., `00000000` = 0.0.0.0, `0100007F` = 127.0.0.1)
- **Port**: Hex port number (e.g., `1F90` = 8080)
- **State**: `0A` indicates LISTEN

### Step 3: Detect Bind Address Issues

Parse the hex address from procfs into a `net.IP`, then check reachability:

```go
// Parse hex address from procfs into net.IP
ip := parseHexIP(hexAddr)  // handles both IPv4 and IPv6 formats

// Only loopback addresses are unreachable via -p
reachable := !ip.IsLoopback()
```

**Reachability rule**: Only loopback addresses (`127.0.0.1`, `::1`) are unreachable via Docker port publishing. All other addresses (including `0.0.0.0`, `::`, container-specific IPs like `172.x.x.x`) are reachable.

If app binds to loopback inside container, surface this diagnostic:

> "App is listening on 127.0.0.1:8080 inside the container, so it can't be reached via published ports. It must bind to 0.0.0.0 (or ::)."

### Step 4: Select Main Container Port

Priority order for selecting the "main" container port:

1. **User selection**: `SelectedContainerPort` if user previously picked one in UI
2. **Config override**: `ContainerPortOverride` in config.json (manual override)
3. **EXPOSE + LISTEN intersection**: Ports that are both exposed in Dockerfile AND detected listening
4. **Preference order intersection**: First match from `[80, 443, 8080, 8000, 3000, 5000, 5173, 4000]` that is detected listening
5. **Lowest numbered**: If no matches, use lowest TCP port that's detected listening

Store all detected ports (not just the selected one) so UI can offer alternatives.

### Step 5: Publish with Docker-Assigned Host Port

Restart container with explicit port mapping, letting Docker choose the host port:

```bash
docker run -d --name maestro-demo \
  -p 127.0.0.1::${CONTAINER_PORT} \
  ...
```

The `127.0.0.1::${PORT}` syntax means:
- Bind to localhost only on host (security)
- Let Docker assign a free host port (no conflicts)
- Map to the specified container port

Retrieve the assigned host port:

```bash
docker port maestro-demo ${CONTAINER_PORT}
# Output: 127.0.0.1:32847
```

### Step 6: TCP Probe Verification

Attempt TCP connect to `127.0.0.1:${HOST_PORT}` with timeout (e.g., 5 seconds).

- **Success**: Connection established → demo is working
- **Failure**: Report diagnostic and try next detected listening port (if any reachable ports remain)

## Config Storage

Add detected port information to `DemoConfig`:

```go
type DemoConfig struct {
    // Container port settings
    ContainerPortOverride     int        `json:"container_port_override"`      // Manual override for container port
    SelectedContainerPort     int        `json:"selected_container_port"`      // User-selected or auto-detected main port
    DetectedPorts             []PortInfo `json:"detected_ports,omitempty"`     // All detected listeners from discovery

    // Host port is NOT persisted - Docker assigns a new one each run
    // LastAssignedHostPort is for display/debug only
    LastAssignedHostPort      int        `json:"last_assigned_host_port,omitempty"`

    // Existing fields
    RunCmdOverride            string     `json:"run_cmd_override"`
    HealthcheckPath           string     `json:"healthcheck_path"`
    HealthcheckTimeoutSeconds int        `json:"healthcheck_timeout_seconds"`
}

type PortInfo struct {
    Port        int    `json:"port"`          // Container port number
    BindAddress string `json:"bind_address"`  // "0.0.0.0", "127.0.0.1", etc.
    Protocol    string `json:"protocol"`      // "tcp", "udp"
    Exposed     bool   `json:"exposed"`       // Was in Dockerfile EXPOSE
    Reachable   bool   `json:"reachable"`     // Can be published (not loopback-bound)
}
```

Note: Host port is dynamically assigned by Docker each run, so we don't persist a fixed host port. `LastAssignedHostPort` is informational only.

## Diagnostics

### Container Exited / Crashed

```
Container exited before opening any listening sockets.
Exit code: 1
Last 10 log lines:
  ...
```

### No Listening Ports Found

```
Container is running, but no TCP ports are listening.
Waited 30 seconds for a listener to appear.
Check that your app starts a server.
```

### Localhost Binding (Most Common Issue)

```
App is listening on 127.0.0.1:8080 inside the container, so it can't
be reached via published ports. It must bind to 0.0.0.0 (or ::).
```

This is purely factual - we don't assume what language/framework the app uses.

### UDP-Only Service

```
Only UDP sockets were detected (no TCP listeners).
Detected: udp 0.0.0.0:5353
Maestro demo currently exposes TCP services only.
```

### Multiple Ports Detected

```
Multiple TCP ports detected. Using port 8080 (highest priority).
Detected ports: 8080, 9090, 3000
You can select a different port in the demo settings.
```

## UI Integration

### Status Panel

```
┌─────────────────────────────────────────────────────┐
│ Demo Status: Running                                 │
├─────────────────────────────────────────────────────┤
│ URL: http://localhost:32847                         │
│ Mapping: localhost:32847 → container:8080           │
│                                                      │
│ Detected Listeners:                                  │
│   ● tcp 0.0.0.0:8080 (selected)                     │
│   ○ tcp 0.0.0.0:9090                                │
│   ⚠ tcp 127.0.0.1:5432 (unreachable - localhost)   │
│                                                      │
│ [Change Port ▼]                                      │
└─────────────────────────────────────────────────────┘
```

### Rebuild Options

```
┌─────────────────────────────────────────────────────┐
│ Rebuild Demo                                         │
├─────────────────────────────────────────────────────┤
│ ☐ Skip port detection (use cached: 8080)            │
│                                                      │
│ [Rebuild]  [Cancel]                                  │
└─────────────────────────────────────────────────────┘
```

## Implementation Phases

### Phase 1: Core Detection

1. Add procfs parsing for `/proc/net/tcp` and `/proc/net/tcp6`
2. Implement bind address detection (0.0.0.0 vs 127.0.0.1)
3. Add port selection logic with preference order
4. Update `DemoConfig` with detected port fields

### Phase 2: Discovery Flow

1. Modify `startContainerOnly` to support discovery mode (no -p)
2. Add polling loop to wait for listeners
3. Implement container restart with detected port
4. Add TCP probe verification

### Phase 3: Caching & Fast Path

1. Save detected ports to config after discovery
2. Use cached port on subsequent starts
3. Fall back to discovery if probe fails

### Phase 4: Rebuild Integration

1. Add "skip detection" parameter to rebuild
2. Re-run discovery by default on rebuild
3. Update cached config after rebuild

### Phase 5: UI Enhancements

1. Display all detected ports in status panel
2. Add port selection UI
3. Show diagnostic messages for common issues
4. Add "skip detection" checkbox to rebuild dialog

## Testing

### Unit Tests

- procfs parsing with various formats
- Bind address detection (IPv4, IPv6, localhost variants)
- Port selection priority logic
- Config serialization/deserialization

### Integration Tests

- Discovery mode with simple HTTP server
- Localhost binding detection
- Multiple port detection
- Rebuild with/without detection skip

### Manual Test Cases

1. App with single port on 0.0.0.0 → should work
2. App binding to 127.0.0.1 → should show diagnostic
3. App with multiple ports → should select by priority
4. App with no EXPOSE but listening → should detect via procfs
5. App that crashes on start → should show exit code + logs
6. Rebuild with port change → should detect new port
7. Rebuild with skip detection → should use cached port
