package ssh

import (
	"errors"
	"fmt"
	"io/ioutil"
)

// SSHConfig represents SSH connection configuration
type SSHConfig struct {
	Host        string
	Port        int
	Username    string
	Password    string
	PrivateKey  string // Path to private key file or PEM content
	KeyContent  []byte // Parsed private key content
	KeyPassword string // Password for decrypting encrypted private keys (not stored)
}

// Validate checks if the SSH configuration is valid
func (c *SSHConfig) Validate() error {
	if c.Host == "" {
		return errors.New("host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return errors.New("invalid port number")
	}
	if c.Username == "" {
		return errors.New("username is required")
	}
	// Allow connection with just password OR just private key OR both
	// No error if one of them is provided
	return nil
}

// LoadPrivateKey loads the private key from file if PrivateKey is a file path
func (c *SSHConfig) LoadPrivateKey() error {
	if c.PrivateKey == "" {
		return nil
	}

	// Check if PrivateKey is already PEM content (starts with -----)
	if len(c.PrivateKey) > 5 && c.PrivateKey[:5] == "-----" {
		c.KeyContent = []byte(c.PrivateKey)
		return nil
	}

	// Try to read as file path
	content, err := ioutil.ReadFile(c.PrivateKey)
	if err != nil {
		return err
	}
	c.KeyContent = content
	return nil
}

// ConnectionID returns a unique identifier for this connection
func (c *SSHConfig) ConnectionID() string {
	return fmt.Sprintf("%s@%s:%d", c.Username, c.Host, c.Port)
}
