#!/bin/sh
# Maestro bootstrap container entrypoint.
# Installs the agentsh shell shim and starts the server before exec'ing the CMD.
# All bootstrap containers (architect, PM, coder fallback) run with agentsh enforcement.
#
# Fail-open: if any agentsh step fails, log a warning and continue.
# A regular shell is always available — agentsh is defense-in-depth, not a gate.

# Install the shell shim (replaces /bin/sh and /bin/bash with policy-aware wrapper).
# Originals are saved as /bin/sh.real and /bin/bash.real.
# v0.10.2+ automatically bypasses the shim for non-interactive (piped) stdin.
if ! agentsh shim install-shell --root / --shim /usr/local/bin/agentsh-shell-shim --bash --i-understand-this-modifies-the-host 2>&1; then
    echo "[maestro-entrypoint] WARNING: agentsh shim install failed, continuing without shim" >&2
fi

# Start the agentsh server in the background
agentsh server start --config "${AGENTSH_CONFIG:-/etc/agentsh/config.yaml}" &
AGENTSH_PID=$!

# Brief wait, then verify the server is running
sleep 0.5
if ! kill -0 "$AGENTSH_PID" 2>/dev/null; then
    echo "[maestro-entrypoint] WARNING: agentsh server not running, continuing without enforcement" >&2
fi

# Exec into the CMD (default: sleep infinity)
exec "$@"
