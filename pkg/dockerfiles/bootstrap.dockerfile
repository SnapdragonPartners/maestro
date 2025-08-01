# Maestro Bootstrap Container
# Lightweight Alpine-based container with tools needed for dockerfile building and troubleshooting
FROM alpine:3.19

# Install essential tools for bootstrap operations
RUN apk add --no-cache \
    # Docker CLI for building user containers
    docker-cli \
    # Git for repository operations and diff generation
    git \
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
    util-linux

# Set bash as default shell
SHELL ["/bin/bash", "-c"]

# Create workspace directory
WORKDIR /workspace

# Default command
CMD ["sleep", "infinity"]