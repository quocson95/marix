package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// LaunchExternalTerminal opens an SSH connection in the OS's default terminal
func LaunchExternalTerminal(host string, port int, username, password, privateKey string) error {
	var cmd *exec.Cmd

	// Handle internal private key content vs path
	keyPath := privateKey
	if privateKey != "" {
		if len(privateKey) > 5 && privateKey[:5] == "-----" {
			// It's content, write to temp file
			tmpFile, err := os.CreateTemp("", "marix-key-*.pem")
			if err != nil {
				return fmt.Errorf("failed to create temp key file: %w", err)
			}

			// Set secure permissions (user-only read/write)
			if err := os.Chmod(tmpFile.Name(), 0600); err != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				return fmt.Errorf("failed to set temp key permissions: %w", err)
			}

			// Write content
			if _, err := tmpFile.WriteString(privateKey); err != nil {
				tmpFile.Close()
				return fmt.Errorf("failed to write temp key file: %w", err)
			}
			tmpFile.Close()
			keyPath = tmpFile.Name()
			// Note: This temporary file is not deleted automatically.
			// Ideally we would delete it after the session, but we assume
			// we detach from the terminal process.
		} else {
			// Expand tilde if present
			if len(privateKey) > 0 && privateKey[0] == '~' {
				home := os.Getenv("HOME")
				if home != "" {
					keyPath = home + privateKey[1:]
				}
			}
		}
	}

	// Build SSH command string for Linux/macOS
	sshCmd := fmt.Sprintf("ssh -p %d %s@%s", port, username, host)
	if keyPath != "" {
		sshCmd = fmt.Sprintf("ssh -i %s -p %d %s@%s", keyPath, port, username, host)
	}

	// Detect OS and use appropriate terminal
	switch runtime.GOOS {
	case "linux":
		// Try different terminal emulators in order of preference
		terminals := []struct {
			name string
			args []string
		}{
			{"gnome-terminal", []string{"--", "bash", "-c", sshCmd + "; exec bash"}},
			{"konsole", []string{"-e", "bash", "-c", sshCmd + "; exec bash"}},
			{"xfce4-terminal", []string{"-e", "bash -c '" + sshCmd + "; exec bash'"}},
			{"xterm", []string{"-e", "bash", "-c", sshCmd + "; exec bash"}},
			{"alacritty", []string{"-e", "bash", "-c", sshCmd + "; exec bash"}},
			{"kitty", []string{"bash", "-c", sshCmd + "; exec bash"}},
			{"terminator", []string{"-e", "bash -c '" + sshCmd + "; exec bash'"}},
		}

		var lastErr error
		for _, term := range terminals {
			// Check if terminal exists
			path, pathErr := exec.LookPath(term.name)
			if pathErr == nil {
				// Use the found path or the name
				cmd = exec.Command(path, term.args...)
				if startErr := cmd.Start(); startErr == nil {
					return nil
				} else {
					lastErr = startErr
				}
			}
		}
		if lastErr != nil {
			return fmt.Errorf("no suitable terminal found: %w", lastErr)
		}
		return fmt.Errorf("no suitable terminal emulator found")

	case "darwin":
		// macOS - use Terminal.app
		script := fmt.Sprintf(`tell application "Terminal"
			do script "%s"
			activate
		end tell`, sshCmd)
		cmd = exec.Command("osascript", "-e", script)
		return cmd.Run()

	case "windows":
		// Windows - use default terminal or Windows Terminal if available

		// Build array args for Windows
		// Default: ssh -p PORT user@host
		args := []string{"ssh", "-p", fmt.Sprintf("%d", port)}

		if keyPath != "" {
			args = append(args, "-i", keyPath)
		}

		args = append(args, fmt.Sprintf("%s@%s", username, host))

		if _, err := exec.LookPath("wt.exe"); err == nil {
			// wt.exe [args to ssh...]
			cmd = exec.Command("wt.exe", args...)
		} else {
			// cmd /c start ssh ...
			// args must be passed to start
			cmdArgs := []string{"/c", "start"}
			cmdArgs = append(cmdArgs, args...)
			cmd = exec.Command("cmd", cmdArgs...)
		}
		return cmd.Run()

	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}
