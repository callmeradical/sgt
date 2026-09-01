package runner

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestSuperviseGroup(t *testing.T) {
	// We want to test that a command that creates children processes
	// gets properly killed and Wait doesn't hang.
	// Context with short timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 10")
	var buf bytes.Buffer
	cmd.Stdout = &buf // Setting Stdout means Wait will wait for pipes to close

	superviseGroup(cmd)

	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start command: %v", err)
	}

	err := cmd.Wait()
	duration := time.Since(start)

	if err == nil {
		t.Errorf("Expected command to fail due to context timeout, but it succeeded")
	}

	if duration > 5*time.Second {
		t.Errorf("superviseGroup failed to prevent hang. Took %v, which is > 5s. WaitDelay or process group kill failed.", duration)
	}
}

func TestSuperviseGroup_NilProcess(t *testing.T) {
	// Verify that Cancel doesn't panic and returns nil when cmd.Process is nil
	cmd := exec.Command("sh", "-c", "echo")
	superviseGroup(cmd)

	if cmd.Process != nil {
		t.Fatalf("Expected cmd.Process to be nil before Start, but it is not")
	}

	err := cmd.Cancel()
	if err != nil {
		t.Errorf("Expected nil error when cancelling cmd with nil Process, got %v", err)
	}
}
