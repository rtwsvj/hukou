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
