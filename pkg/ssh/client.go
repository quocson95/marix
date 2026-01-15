package ssh

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Client manages SSH connections
type Client struct {
	config    *SSHConfig
	client    *ssh.Client
	session   *ssh.Session
	stdin     io.WriteCloser
	stdout    io.Reader
	stderr    io.Reader
	mu        sync.Mutex
	connected bool
	onData    func([]byte)
	onClose   func()
}

// NewClient creates a new SSH client
func NewClient(config *SSHConfig) *Client {
	return &Client{
		config: config,
	}
}

// getHostKeyCallback returns a proper host key verification callback
func getHostKeyCallback() ssh.HostKeyCallback {
	// Try to use known_hosts file
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback to insecure if can't get home dir
		return ssh.InsecureIgnoreHostKey()
	}

	knownHostsPath := filepath.Join(homeDir, ".ssh", "known_hosts")

	// Create .ssh directory if it doesn't exist
	sshDir := filepath.Join(homeDir, ".ssh")
	if _, err := os.Stat(sshDir); os.IsNotExist(err) {
		os.MkdirAll(sshDir, 0700)
	}

	// Create known_hosts file if it doesn't exist
	if _, err := os.Stat(knownHostsPath); os.IsNotExist(err) {
		// Create empty known_hosts file
		f, err := os.OpenFile(knownHostsPath, os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			// Fallback to insecure if can't create file
			return ssh.InsecureIgnoreHostKey()
		}
		f.Close()
	}

	// Use known_hosts for verification
	hostKeyCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		// Fallback to insecure if known_hosts is invalid
		return ssh.InsecureIgnoreHostKey()
	}

	return hostKeyCallback
}

// Connect establishes SSH connection
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.config.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// Load private key if specified
	if err := c.config.LoadPrivateKey(); err != nil {
		return fmt.Errorf("failed to load private key: %w", err)
	}

	// Configure SSH authentication
	var authMethods []ssh.AuthMethod
	if c.config.Password != "" {
		authMethods = append(authMethods, ssh.Password(c.config.Password))
	}
	if len(c.config.KeyContent) > 0 {
		signer, err := ssh.ParsePrivateKey(c.config.KeyContent)
		if err != nil {
			// Try with KeyPassword if available
			if c.config.KeyPassword != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase(c.config.KeyContent, []byte(c.config.KeyPassword))
			}

			if err != nil {
				return fmt.Errorf("failed to parse private key: %w", err)
			}
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	// SSH client config
	sshConfig := &ssh.ClientConfig{
		User:            c.config.Username,
		Auth:            authMethods,
		HostKeyCallback: getHostKeyCallback(),
		Timeout:         30 * time.Second,
	}

	// Connect to SSH server
	addr := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return fmt.Errorf("failed to dial: %w", err)
	}

	c.client = client
	c.connected = true
	return nil
}

// CreateShell creates an interactive shell session
func (c *Client) CreateShell(cols, rows int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return fmt.Errorf("not connected")
	}

	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Set up terminal modes
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	// Request PTY
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		session.Close()
		return fmt.Errorf("failed to request pty: %w", err)
	}

	// Get stdin/stdout/stderr pipes
	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdin: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdout: %w", err)
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stderr: %w", err)
	}

	// Start shell
	if err := session.Shell(); err != nil {
		session.Close()
		return fmt.Errorf("failed to start shell: %w", err)
	}

	c.session = session
	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr

	// Start reading output in goroutines
	go c.readOutput(stdout)
	go c.readOutput(stderr)
	go c.waitForExit()

	return nil
}

// readOutput reads from output stream and calls onData callback
func (c *Client) readOutput(r io.Reader) {
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 && c.onData != nil {
			c.onData(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// waitForExit waits for session to exit
func (c *Client) waitForExit() {
	if c.session != nil {
		c.session.Wait()
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
		if c.onClose != nil {
			c.onClose()
		}
	}
}

// Write sends data to the shell
func (c *Client) Write(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.stdin == nil {
		return fmt.Errorf("not connected or no active shell")
	}

	_, err := c.stdin.Write(data)
	return err
}

// Resize resizes the terminal
func (c *Client) Resize(cols, rows int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session == nil {
		return fmt.Errorf("no active session")
	}

	return c.session.WindowChange(rows, cols)
}

// Execute runs a command and returns the output
func (c *Client) Execute(command string) (string, error) {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return "", fmt.Errorf("not connected")
	}
	c.mu.Unlock()

	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(command)
	if err != nil {
		return "", fmt.Errorf("command failed: %w", err)
	}

	return string(output), nil
}

// OnData sets the callback for receiving output data
func (c *Client) OnData(callback func([]byte)) {
	c.onData = callback
}

// OnClose sets the callback for connection close
func (c *Client) OnClose(callback func()) {
	c.onClose = callback
}

// Close closes the SSH connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.connected = false

	if c.session != nil {
		c.session.Close()
		c.session = nil
	}

	if c.client != nil {
		c.client.Close()
		c.client = nil
	}

	return nil
}

// IsConnected returns true if connected
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// GetRawClient returns the underlying SSH client for SFTP usage
func (c *Client) GetRawClient() *ssh.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

// GetConfig returns the SSH configuration
func (c *Client) GetConfig() *SSHConfig {
	return c.config
}
