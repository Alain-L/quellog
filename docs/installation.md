# Installation Guide

This guide covers all installation methods for quellog across different platforms and environments.

## System Requirements

- **Architecture**: x86_64 (amd64), ARM64 (aarch64)
- **Operating Systems**: Linux, macOS, Windows
- **Disk Space**: ~10 MB for binary
- **Memory**: Varies by log size (typically 50-500 MB)

## Installation Methods

### Pre-built Binaries (Recommended)

The easiest way to install quellog is using pre-built binaries from the GitHub releases page.

#### Linux

=== "x86_64 (amd64)"

    ```bash
    # Download latest release
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    wget "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION}_linux_amd64.tar.gz"

    # Extract
    tar -xzf quellog_${LATEST_VERSION}_linux_amd64.tar.gz

    # Install globally
    sudo install -m 755 quellog /usr/local/bin/quellog

    # Verify installation
    quellog --version
    ```

=== "ARM64 (aarch64)"

    ```bash
    # Download latest release
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    wget "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION}_linux_arm64.tar.gz"

    # Extract
    tar -xzf quellog_${LATEST_VERSION}_linux_arm64.tar.gz

    # Install globally
    sudo install -m 755 quellog /usr/local/bin/quellog

    # Verify installation
    quellog --version
    ```

#### macOS

=== "Intel (x86_64)"

    ```bash
    # Download latest release
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    curl -LO "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION}_darwin_amd64.tar.gz"

    # Extract
    tar -xzf quellog_${LATEST_VERSION}_darwin_amd64.tar.gz

    # Install globally
    sudo install -m 755 quellog /usr/local/bin/quellog

    # Verify installation
    quellog --version
    ```

=== "Apple Silicon (ARM64)"

    ```bash
    # Download latest release
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    curl -LO "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION}_darwin_arm64.tar.gz"

    # Extract
    tar -xzf quellog_${LATEST_VERSION}_darwin_arm64.tar.gz

    # Install globally
    sudo install -m 755 quellog /usr/local/bin/quellog

    # Verify installation
    quellog --version
    ```

#### Windows

=== "x86_64 (amd64)"

    1. Download the latest Windows release from the [releases page](https://github.com/Alain-L/quellog/releases)
    2. Extract `quellog_Windows_x86_64.zip`
    3. Move `quellog.exe` to a directory in your PATH (e.g., `C:\Program Files\quellog\`)
    4. Add the directory to your PATH environment variable
    5. Open a new command prompt and verify:

    ```cmd
    quellog --version
    ```

=== "ARM64"

    1. Download the latest Windows ARM64 release from the [releases page](https://github.com/Alain-L/quellog/releases)
    2. Extract `quellog_Windows_arm64.zip`
    3. Move `quellog.exe` to a directory in your PATH (e.g., `C:\Program Files\quellog\`)
    4. Add the directory to your PATH environment variable
    5. Open a new command prompt and verify:

    ```cmd
    quellog --version
    ```

### Package Managers

#### Linux Package Repositories

=== "Debian/Ubuntu (.deb)"

    ```bash
    # Download the .deb package
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    wget "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION#v}_amd64.deb"

    # Install
    sudo dpkg -i quellog_${LATEST_VERSION#v}_amd64.deb

    # If dependencies are missing
    sudo apt-get install -f

    # Verify installation
    quellog --version
    ```

=== "Red Hat/Fedora/CentOS (.rpm)"

    ```bash
    # Download the .rpm package
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    wget "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION#v}_amd64.rpm"

    # Install (Fedora/RHEL 8+)
    sudo dnf install quellog_${LATEST_VERSION#v}_amd64.rpm

    # Or for older versions
    sudo yum install quellog_${LATEST_VERSION#v}_amd64.rpm

    # Verify installation
    quellog --version
    ```

#### Homebrew (macOS)

!!! warning "Coming Soon"
    Homebrew support is planned but not yet available. Use the binary installation method above.

    ```bash
    # Future usage (not yet available)
    # brew install quellog
    ```

### Build from Source

Building from source gives you the latest development version and allows customization.

#### Prerequisites

- **Go**: version 1.21 or later
- **Git**: for cloning the repository

#### Clone and Build

```bash
# Clone the repository
git clone https://github.com/Alain-L/quellog.git
cd quellog

# Build
go build -o quellog .

# Install globally
sudo install -m 755 quellog /usr/local/bin/quellog

# Verify installation
quellog --version
```

#### Build for Multiple Platforms

Cross-compile for different platforms:

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o quellog-linux-amd64 .

# Linux ARM64
GOOS=linux GOARCH=arm64 go build -o quellog-linux-arm64 .

# macOS AMD64
GOOS=darwin GOARCH=amd64 go build -o quellog-darwin-amd64 .

# macOS ARM64 (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o quellog-darwin-arm64 .

# Windows AMD64
GOOS=windows GOARCH=amd64 go build -o quellog-windows-amd64.exe .
```

#### Development Build

For development with debugging symbols:

```bash
# Build with race detector and debugging
go build -race -o quellog-debug .

# Build with verbose output
go build -v -o quellog .

# Run tests
go test ./...

# Run tests with coverage
go test -cover ./...
```

## Verification

After installation, verify that quellog is working correctly:

```bash
# Check version
quellog --version

# Display help
quellog --help

# Run on a test file (if available)
quellog test/testdata/test_summary.log
```

Expected output for `--version`:

```
quellog version v1.0.0 (commit: abc123def, built: 2025-01-13)
```

## Next Steps

Now that quellog is installed, you're ready to start analyzing your PostgreSQL logs!

- [Quick Start Guide](index.md#quick-start) - Your first log analysis in 5 minutes
- [PostgreSQL Setup](postgresql-setup.md) - Configure PostgreSQL for optimal logging
- [Supported Formats](formats.md) - Learn about log format detection
