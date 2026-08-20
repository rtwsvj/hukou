//go:build unix

package executor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rtwsvj/hukou/internal/orchestrate"
)

// TestRunManager_CancelKillsWholeProcessGroup: the manager spawns a
// backgrounded grandchild (exactly the brew-curl failure shape); when the run
// is cancelled — the timeout and Ctrl-C paths share this same Cancel hook —
// the grandchild's PID must die too: the group SIGTERM/SIGKILL reaches it
// instead of orphaning it.
func TestRunManager_CancelKillsWholeProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	spawn := writeScript(t, dir, "spawn.sh",
		"#!/bin/sh\nsleep 300 &\necho $! > "+pidFile+"\nsleep 300\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = time.Minute // the parent cancel below fires first

	ctx, cancel := context.WithCancel(context.Background())
	resc := make(chan orchestrate.StepResult, 1)
	go func() {
		resc <- e.RunManager(ctx, "brew", [][]string{{spawn}})
	}()

	// Wait until the manager is definitely up and has recorded its grandchild,
	// THEN cancel — asserting on a fixed short timeout instead would race the
	// script's own startup.
	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, err := os.ReadFile(pidFile); err == nil {
			if p, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && p > 0 {
				pid = p
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("manager never wrote its grandchild pid")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	res := <-resc
	if res.Status != orchestrate.StatusCanceled {
		t.Fatalf("status = %s, want canceled (err=%v)", res.Status, res.Err)
	}

	// SIGTERM usually reaps the grandchild immediately; the SIGKILL escalation
	// is the backstop. Poll instead of asserting one fixed instant.
	deadline = time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if err == syscall.ESRCH {
			return // grandchild is gone: the group kill worked
		}
		if err != nil && err != syscall.EPERM {
			t.Fatalf("probe grandchild pid %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild pid %d still alive 5s after cancel; the group kill did not reach it", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
