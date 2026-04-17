// Package sshclient — port forwarding support.
//
// This file implements local and remote SSH port forwarding (tunnelling).
// Local forwarding listens on a local address and pipes connections through
// the SSH channel to a remote destination. Remote forwarding requests the
// SSH server to listen on a remote address and pipes accepted connections
// back to a local destination.
package sshclient

import (
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/andydixon/nexus/internal/models"
	"golang.org/x/crypto/ssh"
)

// ActiveForward tracks a running port-forwarding tunnel.
type ActiveForward struct {
	Rule     models.PortForward `json:"rule"`
	Listener net.Listener       `json:"-"`
	done     chan struct{}
	mu       sync.Mutex
	conns    []net.Conn // tracked connections for clean shutdown
}

// ForwardManager manages active port-forwarding tunnels for a single SSH connection.
type ForwardManager struct {
	mu       sync.Mutex
	forwards map[string]*ActiveForward
	client   *ssh.Client
}

// NewForwardManager creates a ForwardManager bound to the given SSH client.
func NewForwardManager(client *ssh.Client) *ForwardManager {
	return &ForwardManager{
		forwards: make(map[string]*ActiveForward),
		client:   client,
	}
}

// StartForward activates a port-forwarding rule. Returns an error if the
// rule's ID is already active or the listener cannot be established.
func (fm *ForwardManager) StartForward(rule models.PortForward) error {
	fm.mu.Lock()
	if _, exists := fm.forwards[rule.ID]; exists {
		fm.mu.Unlock()
		return fmt.Errorf("forward %q is already active", rule.ID)
	}
	fm.mu.Unlock()

	switch rule.Direction {
	case models.PortForwardLocal:
		return fm.startLocalForward(rule)
	case models.PortForwardRemote:
		return fm.startRemoteForward(rule)
	default:
		return fmt.Errorf("unsupported forward direction %q", rule.Direction)
	}
}

// StopForward terminates a specific active forward.
func (fm *ForwardManager) StopForward(forwardID string) error {
	fm.mu.Lock()
	af, ok := fm.forwards[forwardID]
	if !ok {
		fm.mu.Unlock()
		return fmt.Errorf("forward %q not found", forwardID)
	}
	delete(fm.forwards, forwardID)
	fm.mu.Unlock()

	return af.shutdown()
}

// StopAll terminates every active forward. Called on session close.
func (fm *ForwardManager) StopAll() {
	fm.mu.Lock()
	all := make([]*ActiveForward, 0, len(fm.forwards))
	for id, af := range fm.forwards {
		all = append(all, af)
		delete(fm.forwards, id)
	}
	fm.mu.Unlock()

	for _, af := range all {
		_ = af.shutdown()
	}
}

// ActiveForwards returns the list of currently active forwarding rules.
func (fm *ForwardManager) ActiveForwards() []models.PortForward {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	result := make([]models.PortForward, 0, len(fm.forwards))
	for _, af := range fm.forwards {
		result = append(result, af.Rule)
	}
	return result
}

// ---------------------------------------------------------------------------
// Local forwarding: listen locally → tunnel to remote via SSH
// ---------------------------------------------------------------------------

func (fm *ForwardManager) startLocalForward(rule models.PortForward) error {
	bindAddr := rule.BindHost
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	listenAddr := net.JoinHostPort(bindAddr, fmt.Sprintf("%d", rule.BindPort))

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("local forward: listen on %s failed: %w", listenAddr, err)
	}

	af := &ActiveForward{
		Rule:     rule,
		Listener: listener,
		done:     make(chan struct{}),
	}

	fm.mu.Lock()
	fm.forwards[rule.ID] = af
	fm.mu.Unlock()

	destAddr := net.JoinHostPort(rule.DestHost, fmt.Sprintf("%d", rule.DestPort))

	go func() {
		defer close(af.done)
		for {
			localConn, acceptErr := listener.Accept()
			if acceptErr != nil {
				// Listener closed — normal shutdown path.
				return
			}

			af.mu.Lock()
			af.conns = append(af.conns, localConn)
			af.mu.Unlock()

			go func(lc net.Conn) {
				defer lc.Close()

				remoteConn, dialErr := fm.client.Dial("tcp", destAddr)
				if dialErr != nil {
					return
				}
				defer remoteConn.Close()

				af.mu.Lock()
				af.conns = append(af.conns, remoteConn)
				af.mu.Unlock()

				pipeBidirectional(lc, remoteConn)
			}(localConn)
		}
	}()

	return nil
}

// ---------------------------------------------------------------------------
// Remote forwarding: ask server to listen → tunnel back to local destination
// ---------------------------------------------------------------------------

func (fm *ForwardManager) startRemoteForward(rule models.PortForward) error {
	bindAddr := rule.BindHost
	if bindAddr == "" {
		bindAddr = "0.0.0.0"
	}
	listenAddr := net.JoinHostPort(bindAddr, fmt.Sprintf("%d", rule.BindPort))

	listener, err := fm.client.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("remote forward: server listen on %s failed: %w", listenAddr, err)
	}

	af := &ActiveForward{
		Rule:     rule,
		Listener: listener,
		done:     make(chan struct{}),
	}

	fm.mu.Lock()
	fm.forwards[rule.ID] = af
	fm.mu.Unlock()

	destAddr := net.JoinHostPort(rule.DestHost, fmt.Sprintf("%d", rule.DestPort))

	go func() {
		defer close(af.done)
		for {
			remoteConn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			af.mu.Lock()
			af.conns = append(af.conns, remoteConn)
			af.mu.Unlock()

			go func(rc net.Conn) {
				defer rc.Close()

				localConn, dialErr := net.Dial("tcp", destAddr)
				if dialErr != nil {
					return
				}
				defer localConn.Close()

				af.mu.Lock()
				af.conns = append(af.conns, localConn)
				af.mu.Unlock()

				pipeBidirectional(rc, localConn)
			}(remoteConn)
		}
	}()

	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// shutdown closes the listener and all tracked connections.
func (af *ActiveForward) shutdown() error {
	var firstErr error
	if af.Listener != nil {
		if err := af.Listener.Close(); err != nil {
			firstErr = err
		}
	}

	af.mu.Lock()
	conns := af.conns
	af.conns = nil
	af.mu.Unlock()

	for _, c := range conns {
		_ = c.Close()
	}
	return firstErr
}

// pipeBidirectional copies data between two connections until either side closes.
func pipeBidirectional(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	copy := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		// Signal the other direction to stop by closing the write-half.
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}

	go copy(a, b)
	go copy(b, a)
	wg.Wait()
}
