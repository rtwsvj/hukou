package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/rtwsvj/hukou/internal/manifest"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

func cloneManifest(m *manifest.Manifest) *manifest.Manifest {
	if m == nil {
		return nil
	}
	clone := *m
	clone.Entries = append([]manifest.Entry(nil), m.Entries...)
	return &clone
}

func encodeManifest(m *manifest.Manifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ensureDryRunTransactionClean() error {
	if err := statejournal.CheckClean(dataRoot()); err != nil {
		return fmt.Errorf("dry-run cannot recover unfinished state without writing: %w", err)
	}
	return nil
}

func validateTransactionStateSHA(tx *statejournal.Transaction, role, want string, after bool) error {
	var (
		state statejournal.State
		ok    bool
	)
	if after {
		state, ok = tx.After(role)
	} else {
		state, ok = tx.Before(role)
	}
	if !ok {
		return fmt.Errorf("transaction role %q not found", role)
	}
	if state.SHA256 != want {
		return fmt.Errorf("transaction %s SHA-256 mismatch: got %s want %s", role, state.SHA256, want)
	}
	return nil
}

func abortStateTransaction(tx *statejournal.Transaction, operationErr error) error {
	if tx == nil {
		return operationErr
	}
	if abortErr := tx.Abort(); abortErr != nil {
		return errors.Join(operationErr, fmt.Errorf("recover prepared transaction: %w", abortErr))
	}
	return operationErr
}

// commitStateTransaction handles the indeterminate edge where COMMIT creation
// became visible before a sync error. Recover treats the physical marker as the
// decision and converges to one complete side before returning the error.
func commitStateTransaction(tx *statejournal.Transaction) error {
	if err := tx.Commit(); err != nil {
		return errors.Join(err, statejournal.Recover(dataRoot()))
	}
	return nil
}

func finalizeStateTransaction(tx *statejournal.Transaction, stderr io.Writer, name, operation string) {
	if err := tx.Finalize(); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "警告: %s %s 已提交，但清理事务日志失败: %v\n", name, operation, err)
	}
}

func refreshManifest(m *manifest.Manifest) error {
	loaded, err := loadManifest()
	if err != nil {
		return err
	}
	*m = *loaded
	return nil
}
