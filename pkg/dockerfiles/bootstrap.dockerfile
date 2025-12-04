# Maestro Bootstrap Container
# Lightweight Alpine-based container with tools needed for dockerfile building and troubleshooting

# Stage 1: Build the MCP proxy binary
# The proxy runs inside the container and forwards MCP calls to the host via TCP
FROM golang:1.24-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /maestro-mcp-proxy ./cmd/maestro-mcp-proxy

# Stage 2: Final image
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

# Copy MCP proxy binary from builder stage
# The proxy forwards stdio from Claude Code to the TCP server on the host (via host.docker.internal)
COPY --from=builder /maestro-mcp-proxy /usr/local/bin/maestro-mcp-proxy

# Install Claude Code globally for Claude Code mode support
RUN npm install -g @anthropic-ai/claude-code

# Set bash as default shell
SHELL ["/bin/bash", "-c"]

# Create workspace directory
WORKDIR /workspace

# Default command
CMD ["sleep", "infinity"]