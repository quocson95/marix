package tui

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"runtime"
)

// LaunchExternalTerminal opens an SSH connection in the OS's default terminal
func LaunchExternalTerminal(host string, port int, username, password, privateKey string) error {
	var cmd *exec.Cmd

	// Handle internal private key content vs path
	keyPath := privateKey

	slog.Info("New Connection", "host", host, "username", username, "key_len", len(privateKey))

	if privateKey != "" {
		if len(privateKey) > 5 && privateKey[:5] == "-----" {
			// It's content, write to temp file
			tmpFile, err := os.CreateTemp("", "marix-key-*.pem")
			if err != nil {
				return fmt.Errorf("failed to create temp key file: %w", err)
			}

			// Set secure permissions
			// On Windows, we need to close the file before icacls can reliably modify it
			tmpFile.Close()

			if runtime.GOOS == "windows" {
				// On Windows, use icacls to restrict access to the current user
				// /inheritance:r - remove all inherited permissions
				// /grant:r "%USERNAME%":F - grant Full access to current user
				u, err := user.Current()
				var userName string
				if err == nil {
					userName = u.Username
				} else {
					userName = os.Getenv("USERNAME")
				}

				slog.Info("Setting permissions for user", "username", userName, "file", tmpFile.Name())

				cmd := exec.Command("icacls", tmpFile.Name(), "/inheritance:r", "/grant:r", userName+":F")
				// Hide window for the command
				// cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

				output, err := cmd.CombinedOutput()
				if err != nil {
					slog.Error("icacls error", "error", err, "output", string(output))
				} else {
					slog.Info("icacls success", "output", string(output))
				}

				if err != nil {
					os.Remove(tmpFile.Name())
					return fmt.Errorf("failed to secure temp key file on windows: %w, output: %s", err, string(output))
				}
			} else {
				// On Unix-like systems, chmod 600 is sufficient
				if err := os.Chmod(tmpFile.Name(), 0600); err != nil {
					os.Remove(tmpFile.Name())
					return fmt.Errorf("failed to set temp key permissions: %w", err)
				}
			}

			// We need to write content. For that we need to open it again or write before closing.
			// Re-opening is safer permission-wise (file already secured), but writing before closing is standard.
			// Let's re-open to write content
			f, err := os.OpenFile(tmpFile.Name(), os.O_WRONLY|os.O_TRUNC, 0600)
			if err != nil {
				os.Remove(tmpFile.Name())
				return fmt.Errorf("failed to re-open temp key file: %w", err)
			}

			if _, err := f.WriteString(privateKey); err != nil {
				f.Close()
				os.Remove(tmpFile.Name())
				return fmt.Errorf("failed to write temp key file: %w", err)
			}
			f.Close()

			keyPath = tmpFile.Name()
			// Note: This temporary file is not deleted automatically.
			// We cannot delete it immediately because the external terminal needs time to start and read it.
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
			{"gnome-terminal", []string{"--tab", "--", "bash", "-c", sshCmd + "; exec bash"}},
			{"konsole", []string{"--new-tab", "-e", "bash", "-c", sshCmd + "; exec bash"}},
			{"xfce4-terminal", []string{"--tab", "-e", "bash -c '" + sshCmd + "; exec bash'"}},
			{"mate-terminal", []string{"--tab", "--", "bash", "-c", sshCmd + "; exec bash"}},
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
		args := []string{"ssh", "-v", "-p", fmt.Sprintf("%d", port)}

		if keyPath != "" {
			args = append(args, "-i", keyPath)
		}

		args = append(args, fmt.Sprintf("%s@%s", username, host))

		slog.Info("SSH Command Args", "args", args)
		slog.Info("Key Path", "path", keyPath)

		if _, err := exec.LookPath("wt.exe"); err == nil {
			// wt.exe -w 0 nt ssh ...
			// -w 0 targets the current window (if possible/supported context), nt = new-tab
			wtArgs := []string{"-w", "0", "nt"}
			wtArgs = append(wtArgs, args...)
			cmd = exec.Command("wt.exe", wtArgs...)
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
