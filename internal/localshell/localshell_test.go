package localshell

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClientUsesConfiguredDirectoryAndPreStartSize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local PTY test requires a Unix shell")
	}

	dir := t.TempDir()
	startDir := filepath.Join(dir, "working")
	if err := os.Mkdir(startDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "report-terminal.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nstty size\npwd\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	var outputMu sync.Mutex
	closed := make(chan struct{})
	errors := make(chan error, 1)
	client := NewClient(Config{
		Shell:    script,
		StartDir: startDir,
		Cols:     80,
		Rows:     24,
		OnData: func(data []byte) {
			outputMu.Lock()
			_, _ = output.Write(data)
			outputMu.Unlock()
		},
		OnError: func(err error) {
			errors <- err
		},
		OnClose: func() {
			close(closed)
		},
	})

	// A frontend fit can arrive before the process starts. The stored size
	// must become the initial PTY geometry rather than being discarded.
	if err := client.Resize(132, 43); err != nil {
		t.Fatalf("Resize() before Start() error = %v", err)
	}
	if err := client.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	select {
	case err := <-errors:
		t.Fatalf("unexpected PTY read error: %v", err)
	case <-closed:
	case <-time.After(5 * time.Second):
		client.Close()
		t.Fatal("timed out waiting for local shell to exit")
	}

	outputMu.Lock()
	got := strings.ReplaceAll(output.String(), "\r", "")
	outputMu.Unlock()
	if !strings.Contains(got, "43 132\n") {
		t.Fatalf("PTY output %q does not contain configured size 43 132", got)
	}
	if !strings.Contains(got, startDir+"\n") {
		t.Fatalf("PTY output %q does not contain start directory %q", got, startDir)
	}
}

func TestBytesContainCommandBoundary(t *testing.T) {
	for _, data := range [][]byte{[]byte("pwd\r"), []byte("pwd\n"), {0x04}} {
		if !bytesContainCommandBoundary(data) {
			t.Fatalf("bytesContainCommandBoundary(%q) = false, want true", data)
		}
	}
	if bytesContainCommandBoundary([]byte("pwd")) {
		t.Fatal("bytesContainCommandBoundary without enter/EOF = true, want false")
	}
}

func TestClientCapturesWorkingDirectoryBeforeExit(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("process cwd lookup is implemented on macOS and Linux")
	}

	dir := t.TempDir()
	startDir := filepath.Join(dir, "start")
	nextDir := filepath.Join(startDir, "next")
	if err := os.MkdirAll(nextDir, 0o700); err != nil {
		t.Fatal(err)
	}
	expectedDir, err := filepath.EvalSymlinks(nextDir)
	if err != nil {
		t.Fatal(err)
	}

	closed := make(chan struct{})
	client := NewClient(Config{
		Shell:    "/bin/sh",
		StartDir: startDir,
		OnClose: func() {
			close(closed)
		},
	})
	if err := client.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer client.Close()

	if err := client.Write([]byte("cd next\r")); err != nil {
		t.Fatalf("Write(cd) error = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for client.WorkingDirectory() != expectedDir && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		// The empty command boundary snapshots the directory after cd has run.
		if err := client.Write([]byte("\r")); err != nil {
			t.Fatalf("Write(snapshot) error = %v", err)
		}
	}
	if got := client.WorkingDirectory(); got != expectedDir {
		t.Fatalf("WorkingDirectory() = %q, want %q", got, expectedDir)
	}

	if err := client.Write([]byte("exit\r")); err != nil {
		t.Fatalf("Write(exit) error = %v", err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for local shell to exit")
	}
	if got := client.WorkingDirectory(); got != expectedDir {
		t.Fatalf("WorkingDirectory() after exit = %q, want %q", got, expectedDir)
	}
}
