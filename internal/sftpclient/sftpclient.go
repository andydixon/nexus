// Package sftpclient provides an SFTP file browser and transfer capability
// over an established SSH connection. It wraps github.com/pkg/sftp to provide
// directory listing, upload, download, rename, delete, and mkdir operations
// that the frontend can call through the Wails API.
//
// The client is created per-session — each SSH session can have at most one
// SFTP subsystem active.
package sftpclient

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// FileEntry represents a single file or directory in a remote listing.
type FileEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"` // full remote path
	Size       int64  `json:"size"`
	IsDir      bool   `json:"isDir"`
	Mode       string `json:"mode"`       // Unix permission string, e.g. "-rwxr-xr-x"
	ModifiedAt int64  `json:"modifiedAt"` // Unix epoch milliseconds
	Owner      string `json:"owner"`      // best-effort UID
}

// TransferProgress reports the state of a file transfer.
type TransferProgress struct {
	Transferred int64  `json:"transferred"` // bytes transferred so far
	Total       int64  `json:"total"`       // total bytes (0 if unknown)
	Path        string `json:"path"`        // file being transferred
	Done        bool   `json:"done"`
	Error       string `json:"error,omitempty"`
}

// Client wraps an SFTP session.
type Client struct {
	mu     sync.Mutex
	sftp   *sftp.Client
	closed bool
}

// NewClient creates an SFTP subsystem over an existing SSH connection.
func NewClient(sshConn *ssh.Client) (*Client, error) {
	sc, err := sftp.NewClient(sshConn)
	if err != nil {
		return nil, fmt.Errorf("sftpclient: failed to start SFTP subsystem: %w", err)
	}
	return &Client{sftp: sc}, nil
}

// Close shuts down the SFTP subsystem.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true
	if c.sftp != nil {
		return c.sftp.Close()
	}
	return nil
}

// List returns the contents of a remote directory.
func (c *Client) List(remotePath string) ([]FileEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.sftp == nil {
		return nil, fmt.Errorf("sftpclient: client is closed")
	}

	entries, err := c.sftp.ReadDir(remotePath)
	if err != nil {
		return nil, fmt.Errorf("sftpclient: failed to list %s: %w", remotePath, err)
	}

	result := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		fe := FileEntry{
			Name:       e.Name(),
			Path:       path.Join(remotePath, e.Name()),
			Size:       e.Size(),
			IsDir:      e.IsDir(),
			Mode:       e.Mode().String(),
			ModifiedAt: e.ModTime().UnixMilli(),
		}
		result = append(result, fe)
	}

	// Sort: directories first, then alphabetically.
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result, nil
}

// Stat returns information about a single remote path.
func (c *Client) Stat(remotePath string) (*FileEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.sftp == nil {
		return nil, fmt.Errorf("sftpclient: client is closed")
	}

	info, err := c.sftp.Stat(remotePath)
	if err != nil {
		return nil, fmt.Errorf("sftpclient: stat failed for %s: %w", remotePath, err)
	}

	return &FileEntry{
		Name:       info.Name(),
		Path:       remotePath,
		Size:       info.Size(),
		IsDir:      info.IsDir(),
		Mode:       info.Mode().String(),
		ModifiedAt: info.ModTime().UnixMilli(),
	}, nil
}

// Mkdir creates a remote directory.
func (c *Client) Mkdir(remotePath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.sftp == nil {
		return fmt.Errorf("sftpclient: client is closed")
	}

	return c.sftp.Mkdir(remotePath)
}

// MkdirAll creates a remote directory and all parents.
func (c *Client) MkdirAll(remotePath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.sftp == nil {
		return fmt.Errorf("sftpclient: client is closed")
	}

	return c.sftp.MkdirAll(remotePath)
}

// Remove deletes a remote file or empty directory.
func (c *Client) Remove(remotePath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.sftp == nil {
		return fmt.Errorf("sftpclient: client is closed")
	}

	return c.sftp.Remove(remotePath)
}

// Rename moves or renames a remote file/directory.
func (c *Client) Rename(oldPath, newPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.sftp == nil {
		return fmt.Errorf("sftpclient: client is closed")
	}

	return c.sftp.Rename(oldPath, newPath)
}

// Download copies a remote file to a local path. The onProgress callback
// is invoked periodically with transfer progress (may be nil).
func (c *Client) Download(remotePath, localPath string, onProgress func(TransferProgress)) error {
	c.mu.Lock()
	if c.closed || c.sftp == nil {
		c.mu.Unlock()
		return fmt.Errorf("sftpclient: client is closed")
	}

	// Get file size for progress reporting.
	info, err := c.sftp.Stat(remotePath)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("sftpclient: stat failed: %w", err)
	}
	totalSize := info.Size()

	remote, err := c.sftp.Open(remotePath)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("sftpclient: failed to open remote file: %w", err)
	}
	c.mu.Unlock()
	defer remote.Close()

	local, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("sftpclient: failed to create local file: %w", err)
	}
	defer local.Close()

	return c.copyWithProgress(remote, local, totalSize, remotePath, onProgress)
}

// Upload copies a local file to a remote path. The onProgress callback
// is invoked periodically with transfer progress (may be nil).
func (c *Client) Upload(localPath, remotePath string, onProgress func(TransferProgress)) error {
	local, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("sftpclient: failed to open local file: %w", err)
	}
	defer local.Close()

	info, err := local.Stat()
	if err != nil {
		return fmt.Errorf("sftpclient: failed to stat local file: %w", err)
	}
	totalSize := info.Size()

	c.mu.Lock()
	if c.closed || c.sftp == nil {
		c.mu.Unlock()
		return fmt.Errorf("sftpclient: client is closed")
	}
	remote, err := c.sftp.Create(remotePath)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("sftpclient: failed to create remote file: %w", err)
	}
	c.mu.Unlock()
	defer remote.Close()

	return c.copyWithProgress(local, remote, totalSize, remotePath, onProgress)
}

// UploadBytes writes in-memory data to a remote file path.
func (c *Client) UploadBytes(remotePath string, data []byte, onProgress func(TransferProgress)) error {
	c.mu.Lock()
	if c.closed || c.sftp == nil {
		c.mu.Unlock()
		return fmt.Errorf("sftpclient: client is closed")
	}
	remote, err := c.sftp.Create(remotePath)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("sftpclient: failed to create remote file: %w", err)
	}
	c.mu.Unlock()
	defer remote.Close()

	reader := bytes.NewReader(data)
	return c.copyWithProgress(reader, remote, int64(len(data)), remotePath, onProgress)
}

// Pwd returns the current working directory on the remote host.
func (c *Client) Pwd() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.sftp == nil {
		return "", fmt.Errorf("sftpclient: client is closed")
	}

	return c.sftp.Getwd()
}

// copyWithProgress copies from src to dst, reporting progress periodically.
func (c *Client) copyWithProgress(src io.Reader, dst io.Writer, totalSize int64, filePath string, onProgress func(TransferProgress)) error {
	buf := make([]byte, 32*1024) // 32 KB buffer
	var transferred int64
	lastReport := time.Now()

	for {
		n, err := src.Read(buf)
		if n > 0 {
			nw, wErr := dst.Write(buf[:n])
			if wErr != nil {
				return fmt.Errorf("sftpclient: write failed: %w", wErr)
			}
			transferred += int64(nw)

			// Report progress at most every 100ms.
			if onProgress != nil && time.Since(lastReport) > 100*time.Millisecond {
				onProgress(TransferProgress{
					Transferred: transferred,
					Total:       totalSize,
					Path:        filePath,
				})
				lastReport = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("sftpclient: read failed: %w", err)
		}
	}

	// Final progress report.
	if onProgress != nil {
		onProgress(TransferProgress{
			Transferred: transferred,
			Total:       totalSize,
			Path:        filePath,
			Done:        true,
		})
	}

	return nil
}
