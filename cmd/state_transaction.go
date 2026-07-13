package cmd

import (
	"errors"
	"fmt"

	"github.com/rtwsvj/hukou/internal/store"
)

func restoreLiveAfterError(snapshot *store.LiveSnapshot, operationErr error) error {
	if snapshot == nil {
		return operationErr
	}
	if restoreErr := snapshot.Restore(); restoreErr != nil {
		return errors.Join(operationErr, fmt.Errorf("restore previous live path: %w", restoreErr))
	}
	return operationErr
}

// discardLiveAfterError is used before activation has completed. At that
// point hukou has not changed the live name, so restoring the snapshot could
// overwrite an external update that happened concurrently.
func discardLiveAfterError(snapshot *store.LiveSnapshot, operationErr error) error {
	if snapshot == nil {
		return operationErr
	}
	if cleanupErr := snapshot.Commit(); cleanupErr != nil {
		return errors.Join(operationErr, fmt.Errorf("discard unused live snapshot: %w", cleanupErr))
	}
	return operationErr
}
