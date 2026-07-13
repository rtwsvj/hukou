package state

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	second, err := Acquire(path)
	if !errors.Is(err, ErrLocked) {
		if second != nil {
			_ = second.Release()
		}
		t.Fatalf("want ErrLocked, got %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	first = nil
	third, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireIsExclusiveAcrossProcesses(t *testing.T) {
	if os.Getenv("HUKOU_LOCK_HELPER") == "1" {
		lock, err := Acquire(os.Getenv("HUKOU_LOCK_PATH"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("locked")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		if err := lock.Release(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		return
	}

	path := filepath.Join(t.TempDir(), "state.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAcquireIsExclusiveAcrossProcesses$")
	cmd.Env = append(os.Environ(), "HUKOU_LOCK_HELPER=1", "HUKOU_LOCK_PATH="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var childErr strings.Builder
	cmd.Stderr = &childErr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("child did not acquire lock: line=%q err=%v stderr=%s", line, err, childErr.String())
	}

	second, err := Acquire(path)
	if !errors.Is(err, ErrLocked) {
		if second != nil {
			_ = second.Release()
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("want cross-process ErrLocked, got %v", err)
	}
	if _, err := fmt.Fprintln(stdin); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("lock helper failed: %v stderr=%s", err, childErr.String())
	}

	third, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire after child exit: %v", err)
	}
	if err := third.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("do-not-touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.lock")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if lock, err := Acquire(path); err == nil {
		_ = lock.Release()
		t.Fatal("expected symlink lock path rejection")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "do-not-touch" {
		t.Fatalf("symlink target was modified: %q", got)
	}
}
