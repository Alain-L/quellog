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

## Docker Installation

!!! info "Coming Soon"
    Docker images will be available in a future release.

    ```bash
    # Future usage (not yet available)
    # docker pull ghcr.io/alain-l/quellog:latest
    # docker run -v /var/log/postgresql:/logs ghcr.io/alain-l/quellog:latest /logs/*.log
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

## Permissions

quellog needs read access to PostgreSQL log files. Depending on your setup:

### Linux/macOS

PostgreSQL logs are typically owned by the `postgres` user. You have several options:

=== "Run with sudo (Quick)"

    ```bash
    sudo quellog /var/log/postgresql/*.log
    ```

=== "Add user to postgres group (Recommended)"

    ```bash
    # Add your user to the postgres group
    sudo usermod -a -G postgres $USER

    # Log out and back in for changes to take effect
    # Then make logs group-readable
    sudo chmod -R g+r /var/log/postgresql/

    # Now you can run without sudo
    quellog /var/log/postgresql/*.log
    ```

=== "Copy logs to accessible location"

    ```bash
    # Copy logs to your home directory
    sudo cp /var/log/postgresql/*.log ~/postgresql-logs/
    sudo chown $USER:$USER ~/postgresql-logs/*

    # Analyze copies
    quellog ~/postgresql-logs/*.log
    ```

### Windows

On Windows, you may need administrator privileges depending on where PostgreSQL stores its logs. Run Command Prompt or PowerShell as Administrator if needed.

## Updating

To update quellog to the latest version:

### Binary Installation

```bash
# Download and replace the binary using the same method as installation
# Example for Linux:
LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
wget "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION}_linux_amd64.tar.gz"
tar -xzf quellog_${LATEST_VERSION}_linux_amd64.tar.gz
sudo install -m 755 quellog /usr/local/bin/quellog
```

### Package Manager

=== "Debian/Ubuntu"

    ```bash
    # Download and install new .deb package
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    wget "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION#v}_amd64.deb"
    sudo dpkg -i quellog_${LATEST_VERSION#v}_amd64.deb
    ```

=== "Red Hat/Fedora/CentOS"

    ```bash
    # Download and install new .rpm package
    LATEST_VERSION=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    wget "https://github.com/Alain-L/quellog/releases/download/${LATEST_VERSION}/quellog_${LATEST_VERSION#v}_amd64.rpm"
    sudo dnf install quellog_${LATEST_VERSION#v}_amd64.rpm
    ```

### Source Build

```bash
# Update repository
cd quellog
git pull origin main

# Rebuild
go build -o quellog .

# Reinstall
sudo install -m 755 quellog /usr/local/bin/quellog
```

## Uninstallation

To remove quellog from your system:

### Binary Installation

```bash
# Remove the binary
sudo rm /usr/local/bin/quellog
```

### Package Installation

=== "Debian/Ubuntu"

    ```bash
    sudo dpkg -r quellog
    ```

=== "Red Hat/Fedora/CentOS"

    ```bash
    sudo dnf remove quellog
    # or
    sudo yum remove quellog
    ```

## Troubleshooting

### Command not found

If you get `command not found` after installation:

1. Check that quellog is in your PATH:

    ```bash
    which quellog
    ```

2. If not found, add `/usr/local/bin` to your PATH:

    ```bash
    export PATH=$PATH:/usr/local/bin

    # Make permanent (add to ~/.bashrc or ~/.zshrc)
    echo 'export PATH=$PATH:/usr/local/bin' >> ~/.bashrc
    ```

### Permission denied

If you get permission errors:

```bash
# Make the binary executable
chmod +x /usr/local/bin/quellog

# Or reinstall with correct permissions
sudo install -m 755 quellog /usr/local/bin/quellog
```

### Cannot read log files

If quellog reports permission errors reading logs:

```bash
# Check file permissions
ls -la /var/log/postgresql/

# Option 1: Run with sudo
sudo quellog /var/log/postgresql/*.log

# Option 2: Add your user to postgres group (see Permissions section above)
```

## Next Steps

Now that quellog is installed, learn how to:

- [Configure PostgreSQL](postgresql-setup.md) for comprehensive logging
- [Understand log formats](formats.md) that quellog supports
- [Start analyzing logs](quick-start.md) with the quick start guide
