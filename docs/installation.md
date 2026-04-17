# Installation

## System Requirements

- **Architecture**: x86_64 (amd64), ARM64 (aarch64)
- **Operating Systems**: Linux, macOS, Windows
- **Disk Space**: ~10 MB
- **Memory**: Typically 50-500 MB depending on log size

## Linux Packages (Recommended)

=== "Debian / Ubuntu"

    ```bash
    LATEST=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"v([^"]+)".*/\1/')
    ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')
    wget "https://github.com/Alain-L/quellog/releases/download/v${LATEST}/quellog_${LATEST}_linux_${ARCH}.deb"
    sudo dpkg -i quellog_${LATEST}_linux_${ARCH}.deb
    ```

=== "Red Hat / Fedora"

    ```bash
    LATEST=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"v([^"]+)".*/\1/')
    ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')
    wget "https://github.com/Alain-L/quellog/releases/download/v${LATEST}/quellog_${LATEST}_linux_${ARCH}.rpm"
    sudo dnf install quellog_${LATEST}_linux_${ARCH}.rpm
    ```

## Pre-built Binaries

For macOS, Windows, or Linux distributions without .deb/.rpm support.

Download from the [releases page](https://github.com/Alain-L/quellog/releases).

=== "Linux"

    ```bash
    LATEST=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"v([^"]+)".*/\1/')
    ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')
    wget "https://github.com/Alain-L/quellog/releases/download/v${LATEST}/quellog_${LATEST}_linux_${ARCH}.tar.gz"
    tar -xzf quellog_${LATEST}_linux_${ARCH}.tar.gz
    sudo install -m 755 quellog /usr/local/bin/quellog
    ```

=== "macOS"

    ```bash
    LATEST=$(curl -s https://api.github.com/repos/Alain-L/quellog/releases/latest | grep '"tag_name":' | sed -E 's/.*"v([^"]+)".*/\1/')
    ARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/arm64/arm64/')
    curl -LO "https://github.com/Alain-L/quellog/releases/download/v${LATEST}/quellog_${LATEST}_darwin_${ARCH}.tar.gz"
    tar -xzf quellog_${LATEST}_darwin_${ARCH}.tar.gz
    sudo install -m 755 quellog /usr/local/bin/quellog
    ```

=== "Windows"

    1. Download the latest `.zip` from the [releases page](https://github.com/Alain-L/quellog/releases)
    2. Extract `quellog.exe`
    3. Move it to a directory in your PATH
    4. Verify: `quellog --version`

## Build from Source

Requires Go 1.21+ and Git.

```bash
git clone https://github.com/Alain-L/quellog.git
cd quellog
go build -o quellog .
sudo install -m 755 quellog /usr/local/bin/quellog
```

## Verify

```bash
quellog --version
```
