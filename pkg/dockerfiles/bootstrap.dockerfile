# Maestro Bootstrap Container
# Lightweight Alpine-based container with tools needed for dockerfile building and troubleshooting
#
# Build context must contain a pre-compiled maestro-mcp-proxy binary.
# At runtime, BuildBootstrapImage() writes the embedded proxy binary to the build context.
# For manual builds: make build-mcp-proxy && \
#   cp pkg/coder/claude/embedded/proxy-linux-$(go env GOARCH) maestro-mcp-proxy && \
#   docker build -t maestro-bootstrap -f pkg/dockerfiles/bootstrap.dockerfile .
FROM alpine:3.19

# Install essential tools for bootstrap operations
RUN apk add --no-cache \
    # Docker CLI for building user containers
    docker-cli \
    # Git for repository operations and diff generation
    git \
    # GitHub CLI for pull request operations and git authentication
    github-cli \
    # Core development and shell tools
    bash \
    curl \
    jq \
    make \
    # Text processing utilities
    findutils \
    grep \
    sed \
    gawk \
    # Text editors for dockerfile troubleshooting
    nano \
    vim \
    # File system utilities
    tree \
    # Additional utilities
    coreutils \
    util-linux \
    # Node.js and npm for Claude Code
    nodejs \
    npm

# Copy MCP proxy binary from build context
# The proxy forwards stdio from Claude Code to the TCP server on the host (via host.docker.internal)
COPY maestro-mcp-proxy /usr/local/bin/maestro-mcp-proxy

# Install Claude Code globally for Claude Code mode support
# Minimum version constraint: v2.1.27-2.1.30 had /resume session hang bugs
RUN npm install -g "@anthropic-ai/claude-code@>=2.1.42"

# Set bash as default shell
SHELL ["/bin/bash", "-c"]

# Create workspace directory first, then create non-root user with proper ownership
WORKDIR /workspace

# Create coder user (UID 1000) for non-root execution
# Claude Code refuses --dangerously-skip-permissions when running as root
# All coders run as this user for security isolation
RUN adduser -D -u 1000 -h /home/coder coder && \
    chown -R coder:coder /workspace && \
    chown -R coder:coder /home/coder

# --- Agentsh security gateway (always active in bootstrap containers) ---
# We install the agentsh BINARIES and config but do NOT install the shell shim here.
# The shim replaces /bin/bash and has no true passthrough mode — if installed at build
# time, it intercepts subsequent RUN commands and fails (server not running during build).
# Instead, the shim is installed at container startup via the entrypoint script.
COPY agentsh/config.yaml /etc/agentsh/config.yaml
ARG TARGETARCH
# Always fetch the latest agentsh release (minimum v0.10.2 for non-interactive stdin bypass).
# Version is resolved at build time via GitHub API so rebuilds pick up new releases automatically.
RUN AGENTSH_VERSION=$(curl -sL https://api.github.com/repos/canyonroad/agentsh/releases/latest | jq -r .tag_name | tr -d v) && \
    echo "Installing agentsh v${AGENTSH_VERSION}" && \
    curl -sL "https://github.com/canyonroad/agentsh/releases/download/v${AGENTSH_VERSION}/agentsh_${AGENTSH_VERSION}_linux_${TARGETARCH}.tar.gz" \
      -o /tmp/agentsh.tar.gz && \
    mkdir -p /etc/agentsh/policies /usr/lib/agentsh && \
    tar -xzf /tmp/agentsh.tar.gz -C /usr/local/bin/ agentsh agentsh-shell-shim libenvshim.so && \
    tar -xzf /tmp/agentsh.tar.gz -C /etc/agentsh/policies/ --strip-components=1 policies/ && \
    tar -xzf /tmp/agentsh.tar.gz -C /usr/lib/agentsh/ --strip-components=1 packaging/ 2>/dev/null; \
    chmod 0755 /usr/local/bin/agentsh /usr/local/bin/agentsh-shell-shim && \
    rm -f /tmp/agentsh.tar.gz

# Maestro-specific policy (placeholder — uses maestro-internal YAML schema that does not
# yet match agentsh v0.10.x expected schema. Needs schema alignment before activation.
# The server config still points to the bundled "default" policy extracted from the tarball.)
COPY agentsh/maestro-policy.yaml /etc/agentsh/policies/maestro.yaml

# Entrypoint script: installs the shim and starts the agentsh server, then execs the CMD.
COPY agentsh/entrypoint.sh /usr/local/bin/maestro-entrypoint.sh
ENTRYPOINT ["/usr/local/bin/maestro-entrypoint.sh"]

# Default command
CMD ["sleep", "infinity"]
