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

// TestRunManager_PromptExitSparesEscalationKill: a manager that dies at once
// on the group SIGTERM must not be followed by the delayed SIGKILL — after
// Wait reaps it, its pgid is spent and blindly signaling it could hit a PID
// the kernel reassigned. The canary below ignores SIGTERM (so only the
// escalated SIGKILL could kill it) yet stays in the manager's process group;
// pre-fix it was slaughtered at termKillGrace, post-fix it survives.
func TestRunManager_PromptExitSparesEscalationKill(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "canary.pid")
	spawn := writeScript(t, dir, "canary.sh",
		"#!/bin/sh\n"+
			"( trap '' TERM\n"+
			"  while :; do sleep 5; done\n"+
			") >/dev/null 2>&1 &\n"+ // detached output: pipes must close with the manager
			"echo $! > "+pidFile+"\n"+
			"exec sleep 300\n")

	var out, errb lockedBuf
	e := New(&out, &errb)
	e.Timeout = time.Minute // the parent cancel below fires first

	ctx, cancel := context.WithCancel(context.Background())
	resc := make(chan orchestrate.StepResult, 1)
	go func() {
		resc <- e.RunManager(ctx, "brew", [][]string{{spawn}})
	}()

	// Wait for the canary to record itself before cancelling, so the timing
	// assertions below measure the escalation, not script startup.
	var canary int
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, err := os.ReadFile(pidFile); err == nil {
			if p, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && p > 0 {
				canary = p
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("manager never wrote its canary pid")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() { _ = syscall.Kill(canary, syscall.SIGKILL) })

	cancelled := time.Now()
	cancel()

	res := <-resc
	if res.Status != orchestrate.StatusCanceled {
		t.Fatalf("status = %s, want canceled (err=%v)", res.Status, res.Err)
	}
	// The manager is `exec sleep 300`, which dies on the group SIGTERM
	// immediately; Wait must return well inside the grace period or the
	// prompt-exit premise of this test does not hold on this machine.
	if waited := time.Since(cancelled); waited >= termKillGrace {
		t.Fatalf("Wait returned %s after cancel (>= grace %s)", waited, termKillGrace)
	}

	// Outlive the escalation timer, then demand the canary is still alive:
	// a reaped group must not receive the delayed SIGKILL.
	time.Sleep(termKillGrace + 600*time.Millisecond)
	switch err := syscall.Kill(canary, 0); err {
	case nil:
		// survived: the reaped group was spared
	case syscall.ESRCH:
		t.Fatalf("canary pid %d was killed by the delayed SIGKILL although Wait had already reaped the manager", canary)
	default:
		t.Fatalf("probe canary pid %d: %v", canary, err)
	}
}

// TestRunManager_TermIgnoringChildStillGetsEscalatedKill: a child that
// ignores SIGTERM (the shape of a wedged manager) must still die — the
// escalation fires SIGKILL at termKillGrace instead of leaving Wait blocked
// forever on a group that shrugged off the polite signal.
func TestRunManager_TermIgnoringChildStillGetsEscalatedKill(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	stubborn := writeScript(t, dir, "stubborn.sh",
		"#!/bin/sh\ntrap '' TERM\necho ready > "+ready+"\nwhile :; do sleep 1; done\n")

	var out, errb lockedBuf
	e := New(&out, &errb)

	ctx, cancel := context.WithCancel(context.Background())
	resc := make(chan orchestrate.StepResult, 1)
	go func() {
		resc <- e.RunManager(ctx, "brew", [][]string{{stubborn}})
	}()

	// Cancel only after the trap is provably installed, so an instant death
	// below can only mean the escalation failed, not a lost startup race.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("stubborn child never signalled readiness")
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancelled := time.Now()
	cancel()
	res := <-resc
	if res.Status != orchestrate.StatusCanceled {
		t.Fatalf("status = %s, want canceled (err=%v)", res.Status, res.Err)
	}
	// The child ignores SIGTERM by construction, so the run can only have
	// ended via the escalated SIGKILL: no sooner than the grace period,
	// and shortly after it.
	if elapsed := time.Since(cancelled); elapsed < termKillGrace-200*time.Millisecond {
		t.Fatalf("stubborn child ended %s after cancel: SIGKILL fired before the grace period elapsed", elapsed)
	}
	if elapsed := time.Since(cancelled); elapsed > termKillGrace+8*time.Second {
		t.Fatalf("stubborn child ended only %s after cancel: escalation timer missing", elapsed)
	}
}
