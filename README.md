# Marix

**Marix** is a powerful, secure, and modern Terminal User Interface (TUI) application for managing SSH connections, transferring files via SFTP, and performing encrypted cloud backups. Built with Go and the Charm ecosystem.

![Marix TUI](./icon.svg)

## ✨ Features

- **🖥️ Server Management**: Organize and manage your SSH servers with ease.
- **🐚 SSH Terminal**: Connect to your servers directly from the TUI.
- **📂 Dual-Pane SFTP**: robust file manager with dual-pane layout (Local <-> Remote).
  - Upload/Download files and directories.
  - Recursive transfers with `rsync`-like functionality.
  - Queue management and progress tracking.
- **🔐 Encrypted Backups**:
  - Backup your configuration and data to AWS S3.
  - **Zero-Knowledge Encryption**: All backups are encrypted locally using **Argon2id** (key derivation) and **AES-256-GCM** (authenticated encryption) before upload.
  - Securely restore your data on any machine.
- **🛡️ Security First**:
  - Master Password protection for sensitive credentials.
  - Secure handling of SSH keys and temporary files (0600 permissions).
  - `known_hosts` verification to prevent MITM attacks.
- **🎨 Modern UI**: Beautiful, responsive interface with custom themes.

## 🚀 Installation

### Prerequisites

- Go 1.25 or higher

### Build from Source

```bash
# Clone the repository
git clone https://github.com/quocson95/marix.git
cd marix

# Install dependencies
go mod tidy

# Build the binary
go build -o marix ./cmd/marix
```

### Run

```bash
./marix
```

## 📖 Usage

### Main Menu

- **Connect to Server**: Select a saved server to open an SSH session.
- **Manage Servers**: Add, edit, or remove server configurations.
- **SFTP Browser**: File transfer interface.
- **Backup & Restore**: Securely backup your app data to S3.
- **Settings**: Configure default port, username, themes, and master password.

### Key Bindings

**General**:

- `↑`/`↓` or `j`/`k`: Navigate lists
- `Enter`: Select / Confirm
- `Esc`: Go back / Cancel
- `Tab`: Switch focus

**SFTP Browser**:

- `Tab`: Switch between Local and Remote panes
- `u`: Upload selected (Local -> Remote)
- `d`: Download selected (Remote -> Local)
- `r`: Refresh directories
- `x` or `Delete`: Delete file/folder
- `C`: Cancel active transfers

**Backup & Restore**:

- `b`: Start Backup
- `r`: Start Restore

## ⚙️ Configuration

Data is stored locally in your user configuration directory (e.g., `~/.config/marix` or `~/.marix` depending on OS/setup).

- `servers.json`: Stores your server list (sensitive fields encrypted if Master Password is set).
- `settings.json`: Application preferences.

## 🛠️ Technology Stack

- **[Bubble Tea](https://github.com/charmbracelet/bubbletea)**: Functional model-update-view TUI framework.
- **[Lip Gloss](https://github.com/charmbracelet/lipgloss)**: Style definitions for nice terminal layouts.
- **[Bubbles](https://github.com/charmbracelet/bubbles)**: TUI components (inputs, lists, etc.).
- **[AWS SDK for Go v2](https://github.com/aws/aws-sdk-go-v2)**: S3 integration.
- **[Paramiko/Go SSH](https://pkg.go.dev/golang.org/x/crypto/ssh)**: SSH and SFTP capabilities.

## 📄 License

MIT License
