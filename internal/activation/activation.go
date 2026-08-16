// Package activation maintains the logical lineage of versions activated for
// a manifest entry. It does not read the clock or write files; callers can
// prepare the complete after-state before publishing a transaction intent.
package activation

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/manifest"
)

const (
	OperationLegacy   = "legacy"
	OperationAdopt    = "adopt"
	OperationUpgrade  = "upgrade"
	OperationRollback = "rollback"
	OperationRepair   = "repair"
)

var ErrNoPreviousActivation = i18n.Errorf("no previous activation")

// NewID returns an opaque identifier suitable for an activation event. Event
// identifiers are independent of transaction identifiers so the manifest
// after-state can be encoded before the transaction journal is created.
func NewID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", i18n.Wrapf("generate activation id: %w", err)
	}
	return "act-" + hex.EncodeToString(random[:]), nil
}

// RecordAdopt creates the root of a new entry's lineage and synchronizes its
// current tag, digest, and timestamps with that event.
func RecordAdopt(entry *manifest.Entry, eventID, activatedAt string) error {
	if entry == nil {
		return i18n.Errorf("nil manifest entry")
	}
	if entry.ActiveActivationID != "" || len(entry.Activations) != 0 {
		return i18n.Errorf("adopt requires an empty activation lineage")
	}
	if err := validateEventInput(eventID, entry.Tag, entry.SHA256, activatedAt); err != nil {
		return err
	}
	event := manifest.ActivationEvent{
		ID:          eventID,
		Operation:   OperationAdopt,
		Tag:         entry.Tag,
		SHA256:      strings.ToLower(entry.SHA256),
		ActivatedAt: activatedAt,
	}
	entry.SHA256 = event.SHA256
	entry.ActiveActivationID = event.ID
	entry.Activations = []manifest.ActivationEvent{event}
	if entry.AdoptedAt == "" {
		entry.AdoptedAt = activatedAt
	}
	entry.UpdatedAt = activatedAt
	if entry.UpdatePolicy.Mode == "" {
		entry.UpdatePolicy = manifest.DefaultUpdatePolicy()
	}
	return nil
}

// RecordUpgrade appends a normal forward activation whose parent is the
// currently active event.
func RecordUpgrade(entry *manifest.Entry, eventID, tag, sha256, activatedAt string) error {
	if entry == nil {
		return i18n.Errorf("nil manifest entry")
	}
	if err := Validate(*entry); err != nil {
		return i18n.Wrapf("validate current activation lineage: %w", err)
	}
	if err := validateEventInput(eventID, tag, sha256, activatedAt); err != nil {
		return err
	}
	if err := validateNewTagBinding(entry.Activations, tag, sha256); err != nil {
		return err
	}
	if _, ok := eventIndex(*entry)[eventID]; ok {
		return i18n.Errorf("activation id %q already exists", eventID)
	}
	event := manifest.ActivationEvent{
		ID:          eventID,
		ParentID:    entry.ActiveActivationID,
		Operation:   OperationUpgrade,
		Tag:         tag,
		SHA256:      strings.ToLower(sha256),
		ActivatedAt: activatedAt,
	}
	appendCurrent(entry, event)
	return nil
}

// Previous returns the next logical rollback target. The result is derived
// from ParentID, never filesystem modification time or chronological slice
// position.
func Previous(entry manifest.Entry) (manifest.ActivationEvent, error) {
	if err := Validate(entry); err != nil {
		return manifest.ActivationEvent{}, err
	}
	index := eventIndex(entry)
	current := entry.Activations[index[entry.ActiveActivationID]]
	if current.ParentID == "" {
		return manifest.ActivationEvent{}, ErrNoPreviousActivation
	}
	return entry.Activations[index[current.ParentID]], nil
}

// FindAncestorByTag returns the nearest logical ancestor with the exact tag.
// The current activation is intentionally excluded.
func FindAncestorByTag(entry manifest.Entry, tag string) (manifest.ActivationEvent, error) {
	if tag == "" {
		return manifest.ActivationEvent{}, i18n.Errorf("empty activation tag")
	}
	ancestors, err := Ancestors(entry, len(entry.Activations))
	if err != nil {
		return manifest.ActivationEvent{}, err
	}
	for _, event := range ancestors {
		if event.Tag == tag {
			return event, nil
		}
	}
	return manifest.ActivationEvent{}, i18n.Errorf("tag %q is not an activation ancestor", tag)
}

// RecordRollback activates a logical ancestor and advances the rollback
// cursor past it. The new event's parent is target.ParentID rather than the
// event being reverted, which makes repeated rollback walk A<-B<-C as C->B->A
// instead of oscillating C->B->C.
func RecordRollback(entry *manifest.Entry, eventID, targetID, activatedAt string) error {
	if entry == nil {
		return i18n.Errorf("nil manifest entry")
	}
	if err := Validate(*entry); err != nil {
		return i18n.Wrapf("validate current activation lineage: %w", err)
	}
	if err := validateID(eventID); err != nil {
		return err
	}
	if _, ok := eventIndex(*entry)[eventID]; ok {
		return i18n.Errorf("activation id %q already exists", eventID)
	}
	if err := validateTimestamp(activatedAt); err != nil {
		return err
	}
	ancestors, err := Ancestors(*entry, len(entry.Activations))
	if err != nil {
		return err
	}
	var target manifest.ActivationEvent
	found := false
	for _, candidate := range ancestors {
		if candidate.ID == targetID {
			target = candidate
			found = true
			break
		}
	}
	if !found {
		return i18n.Errorf("activation %q is not an ancestor of the current version", targetID)
	}
	event := manifest.ActivationEvent{
		ID:          eventID,
		ParentID:    target.ParentID,
		Operation:   OperationRollback,
		Tag:         target.Tag,
		SHA256:      target.SHA256,
		ActivatedAt: activatedAt,
		RevertsID:   entry.ActiveActivationID,
	}
	appendCurrent(entry, event)
	return nil
}

// RecordRestoreOriginal records an explicit activation of hukou's immutable
// original backup. A schema-one manifest cannot prove that original belongs
// to its synthetic lineage, so this is a narrow exception to RecordRollback's
// ancestor-only rule. The new event intentionally has no parent: a later
// implicit rollback must not guess a route back into history.
func RecordRestoreOriginal(entry *manifest.Entry, eventID, sha256, activatedAt string) error {
	if entry == nil {
		return i18n.Errorf("nil manifest entry")
	}
	if err := Validate(*entry); err != nil {
		return i18n.Wrapf("validate current activation lineage: %w", err)
	}
	if err := validateEventInput(eventID, "original", sha256, activatedAt); err != nil {
		return err
	}
	if err := validateNewTagBinding(entry.Activations, "original", sha256); err != nil {
		return err
	}
	if _, ok := eventIndex(*entry)[eventID]; ok {
		return i18n.Errorf("activation id %q already exists", eventID)
	}
	event := manifest.ActivationEvent{
		ID:          eventID,
		Operation:   OperationRollback,
		Tag:         "original",
		SHA256:      strings.ToLower(sha256),
		ActivatedAt: activatedAt,
		RevertsID:   entry.ActiveActivationID,
	}
	appendCurrent(entry, event)
	return nil
}

// Ancestors returns at most limit logical rollback targets, nearest first.
func Ancestors(entry manifest.Entry, limit int) ([]manifest.ActivationEvent, error) {
	if limit < 0 {
		return nil, i18n.Errorf("negative ancestor limit")
	}
	if err := Validate(entry); err != nil {
		return nil, err
	}
	result := make([]manifest.ActivationEvent, 0, min(limit, len(entry.Activations)))
	index := eventIndex(entry)
	current := entry.Activations[index[entry.ActiveActivationID]]
	for current.ParentID != "" && len(result) < limit {
		parent := entry.Activations[index[current.ParentID]]
		result = append(result, parent)
		current = parent
	}
	return result, nil
}

// Validate verifies that the active event and its immutable lineage are
// internally consistent with the entry's current tag and digest.
func Validate(entry manifest.Entry) error {
	if len(entry.Activations) == 0 {
		return i18n.Errorf("activation lineage is empty")
	}
	if entry.ActiveActivationID == "" {
		return i18n.Errorf("active activation id is empty")
	}
	index := make(map[string]int, len(entry.Activations))
	bindings := make(map[string]string, len(entry.Activations))
	for i, event := range entry.Activations {
		if err := validateEventInput(event.ID, event.Tag, event.SHA256, event.ActivatedAt); err != nil {
			return i18n.Wrapf("activation %d: %w", err, i)
		}
		if err := bindTagSHA(bindings, event.Tag, event.SHA256); err != nil {
			return i18n.Wrapf("activation %d: %w", err, i)
		}
		if !knownOperation(event.Operation) {
			return i18n.Errorf("activation %q has unsupported operation %q", event.ID, event.Operation)
		}
		if _, exists := index[event.ID]; exists {
			return i18n.Errorf("duplicate activation id %q", event.ID)
		}
		if event.ParentID != "" {
			if _, exists := index[event.ParentID]; !exists {
				return i18n.Errorf("activation %q has missing or forward parent %q", event.ID, event.ParentID)
			}
		}
		if event.RevertsID != "" {
			if _, exists := index[event.RevertsID]; !exists {
				return i18n.Errorf("activation %q reverts missing or forward event %q", event.ID, event.RevertsID)
			}
		}
		index[event.ID] = i
	}
	activeIndex, exists := index[entry.ActiveActivationID]
	if !exists {
		return i18n.Errorf("active activation %q does not exist", entry.ActiveActivationID)
	}
	if activeIndex != len(entry.Activations)-1 {
		return i18n.Errorf("active activation %q is not the latest event", entry.ActiveActivationID)
	}
	active := entry.Activations[activeIndex]
	if active.Tag != entry.Tag || !strings.EqualFold(active.SHA256, entry.SHA256) {
		return i18n.Errorf("active activation %q does not match entry tag/SHA-256", active.ID)
	}
	return nil
}

func appendCurrent(entry *manifest.Entry, event manifest.ActivationEvent) {
	entry.Activations = append(entry.Activations, event)
	entry.ActiveActivationID = event.ID
	entry.Tag = event.Tag
	entry.SHA256 = event.SHA256
	entry.UpdatedAt = event.ActivatedAt
}

func eventIndex(entry manifest.Entry) map[string]int {
	index := make(map[string]int, len(entry.Activations))
	for i, event := range entry.Activations {
		index[event.ID] = i
	}
	return index
}

func validateEventInput(eventID, tag, sha256, activatedAt string) error {
	if err := validateID(eventID); err != nil {
		return err
	}
	if err := manifest.ValidateActivationTag(tag); err != nil {
		return err
	}
	if !validSHA256(sha256) {
		return i18n.Errorf("invalid activation SHA-256 %q", sha256)
	}
	return validateTimestamp(activatedAt)
}

func validateNewTagBinding(events []manifest.ActivationEvent, tag, sha256 string) error {
	bindings := make(map[string]string, len(events)+1)
	for _, event := range events {
		if err := bindTagSHA(bindings, event.Tag, event.SHA256); err != nil {
			return err
		}
	}
	return bindTagSHA(bindings, tag, sha256)
}

func bindTagSHA(bindings map[string]string, tag, sha256 string) error {
	normalizedSHA := strings.ToLower(sha256)
	if boundSHA, exists := bindings[tag]; exists && boundSHA != normalizedSHA {
		return i18n.Errorf("activation tag %q is already bound to a different SHA-256", tag)
	}
	bindings[tag] = normalizedSHA
	return nil
}

func validateID(id string) error {
	if id == "" {
		return i18n.Errorf("empty activation id")
	}
	if len(id) > 128 {
		return i18n.Errorf("activation id exceeds 128 bytes")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return i18n.Errorf("activation id %q contains an unsupported character", id)
	}
	return nil
}

func validateTimestamp(value string) error {
	if value == "" {
		return i18n.Errorf("empty activation timestamp")
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return i18n.Wrapf("invalid activation timestamp %q: %w", err, value)
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func knownOperation(operation string) bool {
	switch operation {
	case OperationLegacy, OperationAdopt, OperationUpgrade, OperationRollback, OperationRepair:
		return true
	default:
		return false
	}
}
