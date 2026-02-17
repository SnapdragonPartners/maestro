# Maestro Bootstrap Container
# Lightweight Alpine-based container with tools needed for dockerfile building and troubleshooting
#
# Build context must contain a pre-compiled maestro-mcp-proxy binary.
# At runtime, BuildBootstrapImage() writes the embedded proxy binary to the build context.
# For manual builds: go build -o maestro-mcp-proxy ./cmd/maestro-mcp-proxy && \
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

# Default command
CMD ["sleep", "infinity"]
