// Package sshclient — SSH agent integration.
//
// This file provides SSH agent authentication and agent forwarding.
// It connects to the local SSH agent via the SSH_AUTH_SOCK environment
// variable and can both use agent keys for authentication and forward
// the agent channel to the remote host (allowing the remote session
// to authenticate further hops using local keys).
package sshclient

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// connectAgent connects to the local SSH agent via SSH_AUTH_SOCK.
// Returns the agent and a cleanup function, or an error if unavailable.
func connectAgent() (agent.Agent, func(), error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, fmt.Errorf("SSH_AUTH_SOCK not set — SSH agent unavailable")
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to connect to SSH agent at %s: %w", sock, err)
	}

	ag := agent.NewClient(conn)
	cleanup := func() { conn.Close() }
	return ag, cleanup, nil
}

// agentAuthMethod returns an ssh.AuthMethod that delegates authentication
// to the running SSH agent. Returns nil if the agent is unavailable.
func agentAuthMethod() (ssh.AuthMethod, func(), error) {
	ag, cleanup, err := connectAgent()
	if err != nil {
		return nil, nil, err
	}

	signers, err := ag.Signers()
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("SSH agent: failed to list keys: %w", err)
	}

	if len(signers) == 0 {
		cleanup()
		return nil, nil, fmt.Errorf("SSH agent: no keys loaded")
	}

	return ssh.PublicKeysCallback(ag.Signers), cleanup, nil
}

// requestAgentForwarding sets up agent forwarding on the SSH session so that
// the remote host can use local agent keys for further authentication.
// This is equivalent to `ssh -A`.
func requestAgentForwarding(sshClient *ssh.Client, session *ssh.Session) error {
	if err := agent.RequestAgentForwarding(session); err != nil {
		return fmt.Errorf("requesting agent forwarding: %w", err)
	}

	ag, cleanup, err := connectAgent()
	if err != nil {
		return err
	}

	// ForwardToAgent runs in a background goroutine and serves agent
	// requests from the remote side. It stops when the SSH connection closes.
	go func() {
		defer cleanup()
		agent.ForwardToAgent(sshClient, ag)
	}()

	return nil
}
