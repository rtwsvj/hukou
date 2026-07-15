package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rtwsvj/hukou/internal/durablefs"
)

// CurrentSchemaVersion is the newest manifest schema understood by this
// build. Schema versions zero and one are migrated in memory when loaded and
// are always written back as schema two.
const CurrentSchemaVersion = 2

// DefaultRollbackDepth preserves the two activation ancestors immediately
// preceding the active version. The current version and the immutable
// original backup are protected separately and do not count toward this
// depth.
const DefaultRollbackDepth = 2

type UpdateMode string

const (
	// UpdateModeSemver orders release tags using Semantic Versioning.
	UpdateModeSemver UpdateMode = "semver"
	// UpdateModeLegacy preserves the v0.2 GitHub-latest, exact-tag behavior for
	// manifests migrated from schema one. New entries should use semver mode.
	UpdateModeLegacy UpdateMode = "github-latest"
)

type UpdateChannel string

const (
	UpdateChannelStable     UpdateChannel = "stable"
	UpdateChannelPrerelease UpdateChannel = "prerelease"
)

// UpdatePolicy controls how an entry selects an upstream release. PinnedTag,
// when non-empty, takes precedence over Mode and Channel.
type UpdatePolicy struct {
	Mode      UpdateMode    `json:"mode"`
	Channel   UpdateChannel `json:"channel"`
	PinnedTag string        `json:"pinned_tag,omitempty"`
}

// RetentionPolicy controls how many logical activation ancestors remain
// available as rollback targets. A zero depth is valid and means that only the
// current version, a downloaded pin, and original are protected.
type RetentionPolicy struct {
	RollbackDepth int `json:"rollback_depth"`
}

// ActivationEvent is an immutable node in an entry's activation lineage.
// ParentID is the next logical rollback target, not necessarily the previous
// event in chronological order. This distinction prevents repeated rollback
// operations from oscillating between two versions.
type ActivationEvent struct {
	ID          string `json:"id"`
	ParentID    string `json:"parent_id,omitempty"`
	Operation   string `json:"operation"`
	Tag         string `json:"tag"`
	SHA256      string `json:"sha256"`
	ActivatedAt string `json:"activated_at"`
	RevertsID   string `json:"reverts_id,omitempty"`
}

// Entry is a single record in the manifest.
// All time fields are RFC3339 strings supplied by the caller; the library
// never calls time.Now or any time function.
type Entry struct {
	Name               string            `json:"name"`
	Path               string            `json:"path"`
	Repo               string            `json:"repo"`
	Tag                string            `json:"tag"`
	SHA256             string            `json:"sha256"`
	Upstream           string            `json:"upstream"`
	AdoptedAt          string            `json:"adopted_at"`
	UpdatedAt          string            `json:"updated_at"`
	AssetName          string            `json:"asset_name,omitempty"`
	AssetSHA256        string            `json:"asset_sha256,omitempty"`
	ChecksumAsset      string            `json:"checksum_asset,omitempty"`
	ChecksumVerified   bool              `json:"checksum_verified,omitempty"`
	ActiveActivationID string            `json:"active_activation_id"`
	Activations        []ActivationEvent `json:"activations"`
	UpdatePolicy       UpdatePolicy      `json:"update_policy"`
	Retention          *RetentionPolicy  `json:"retention,omitempty"`
}

// Manifest is the top-level document stored as JSON.
type Manifest struct {
	SchemaVersion int             `json:"schema_version"`
	Retention     RetentionPolicy `json:"retention"`
	Entries       []Entry         `json:"entries"`

	// index maps an entry Name to its position in Entries (first occurrence
	// wins, mirroring the linear scan it accelerates). It is an unexported,
	// never-serialized accelerator with two hard rules:
	//
	//  1. Read paths never write it. Get is strictly read-only, so concurrent
	//     read-only use of a loaded manifest is race-free. The index is built
	//     eagerly by Load/Decode/Clone and maintained synchronously by the
	//     mutating methods Put and Remove (whose callers already serialize
	//     mutations under the state lock).
	//  2. It is advisory, never authoritative. Every index hit is re-verified
	//     against Entries before use, and any miss falls back to the original
	//     linear scan. Code that manipulates the exported Entries slice
	//     directly (or constructs a Manifest literal, leaving index nil)
	//     therefore degrades to the exact pre-index behavior instead of ever
	//     observing a stale or wrong lookup.
	index map[string]int
}

// manifestEnvelope keeps schema-specific fields opaque until schema_version
// has selected the decoder that is allowed to understand them. This prevents
// a legacy document from smuggling v2-only state that migration would
// otherwise silently overwrite.
type manifestEnvelope struct {
	SchemaVersion json.RawMessage `json:"schema_version"`
	Retention     json.RawMessage `json:"retention"`
	Entries       json.RawMessage `json:"entries"`
}

// legacyEntry is the complete schema-zero/one entry shape. V2-only fields are
// deliberately absent so DisallowUnknownFields rejects a falsely downgraded
// document instead of letting migrateV1 discard its policy or history.
type legacyEntry struct {
	Name             string `json:"name"`
	Path             string `json:"path"`
	Repo             string `json:"repo"`
	Tag              string `json:"tag"`
	SHA256           string `json:"sha256"`
	Upstream         string `json:"upstream"`
	AdoptedAt        string `json:"adopted_at"`
	UpdatedAt        string `json:"updated_at"`
	AssetName        string `json:"asset_name,omitempty"`
	AssetSHA256      string `json:"asset_sha256,omitempty"`
	ChecksumAsset    string `json:"checksum_asset,omitempty"`
	ChecksumVerified bool   `json:"checksum_verified,omitempty"`
}

type decodedRetention struct {
	RollbackDepth *int `json:"rollback_depth"`
}

// Load reads a regular manifest file without following a symlink. If the file
// does not exist an empty current-schema manifest is returned (no error).
// Any JSON decode error or unknown schema_version is returned as an error.
func Load(path string) (*Manifest, error) {
	data, _, err := readRegularFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			empty := &Manifest{
				SchemaVersion: CurrentSchemaVersion,
				Retention:     DefaultRetentionPolicy(),
				Entries:       make([]Entry, 0),
			}
			empty.reindex()
			return empty, nil
		}
		return nil, err
	}
	return Decode(data)
}

// Decode parses, migrates, and validates one complete manifest document using
// the same strict boundary as Load. Unknown fields and trailing JSON values are
// rejected so audit and repair callers cannot disagree with normal commands.
func Decode(data []byte) (*Manifest, error) {
	envelope, err := decodeManifestEnvelope(data)
	if err != nil {
		return nil, err
	}
	m, err := decodeManifestForSchema(*envelope)
	if err != nil {
		return nil, err
	}
	if m.SchemaVersion == CurrentSchemaVersion {
		if err := validateDecodedV2(*m); err != nil {
			return nil, fmt.Errorf("validate schema v2 manifest: %w", err)
		}
	}
	if err := m.Normalize(); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	// Build the lookup accelerator eagerly on the fully validated document so
	// later concurrent read-only Gets never have to write manifest state.
	m.reindex()
	return m, nil
}

func decodeManifestEnvelope(data []byte) (*manifestEnvelope, error) {
	var envelope *manifestEnvelope
	if err := decodeStrictJSON(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if envelope == nil {
		return nil, errors.New("decode manifest: document must be an object")
	}
	return envelope, nil
}

func decodeManifestForSchema(envelope manifestEnvelope) (*Manifest, error) {
	schemaVersion, err := decodeSchemaVersion(envelope.SchemaVersion)
	if err != nil {
		return nil, fmt.Errorf("decode manifest schema_version: %w", err)
	}
	if schemaVersion < 0 || schemaVersion > CurrentSchemaVersion {
		return nil, fmt.Errorf("unsupported schema_version %d (current %d)", schemaVersion, CurrentSchemaVersion)
	}
	m := &Manifest{SchemaVersion: schemaVersion}
	switch schemaVersion {
	case 0, 1:
		if len(envelope.Retention) != 0 {
			return nil, fmt.Errorf("decode schema v%d manifest: v2-only top-level field %q is not allowed", schemaVersion, "retention")
		}
		entries, err := decodeLegacyEntries(envelope.Entries, schemaVersion)
		if err != nil {
			return nil, err
		}
		m.Entries = entries
	case CurrentSchemaVersion:
		retention, err := decodeRequiredV2Retention(envelope.Retention)
		if err != nil {
			return nil, fmt.Errorf("decode schema v2 manifest: %w", err)
		}
		entries, err := decodeRequiredV2Entries(envelope.Entries)
		if err != nil {
			return nil, fmt.Errorf("decode schema v2 manifest: %w", err)
		}
		m.Retention = retention
		m.Entries = entries
	}
	return m, nil
}

func decodeSchemaVersion(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, errors.New("must be an integer, not null")
	}
	var schemaVersion int
	if err := decodeStrictJSON(raw, &schemaVersion); err != nil {
		return 0, err
	}
	return schemaVersion, nil
}

func decodeLegacyEntries(raw json.RawMessage, schemaVersion int) ([]Entry, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return make([]Entry, 0), nil
	}
	var encoded []json.RawMessage
	if err := decodeStrictJSON(raw, &encoded); err != nil {
		return nil, fmt.Errorf("decode schema v%d entries: %w", schemaVersion, err)
	}
	entries := make([]Entry, len(encoded))
	for i, value := range encoded {
		var legacy legacyEntry
		if err := decodeStrictJSON(value, &legacy); err != nil {
			return nil, fmt.Errorf("decode schema v%d entry %d: %w", schemaVersion, i, err)
		}
		entries[i] = Entry{
			Name:             legacy.Name,
			Path:             legacy.Path,
			Repo:             legacy.Repo,
			Tag:              legacy.Tag,
			SHA256:           legacy.SHA256,
			Upstream:         legacy.Upstream,
			AdoptedAt:        legacy.AdoptedAt,
			UpdatedAt:        legacy.UpdatedAt,
			AssetName:        legacy.AssetName,
			AssetSHA256:      legacy.AssetSHA256,
			ChecksumAsset:    legacy.ChecksumAsset,
			ChecksumVerified: legacy.ChecksumVerified,
		}
	}
	return entries, nil
}

func decodeRequiredV2Retention(raw json.RawMessage) (RetentionPolicy, error) {
	if len(raw) == 0 {
		return RetentionPolicy{}, errors.New("missing required top-level field \"retention\"")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return RetentionPolicy{}, errors.New("top-level field \"retention\" must be an object")
	}
	var decoded decodedRetention
	if err := decodeStrictJSON(raw, &decoded); err != nil {
		return RetentionPolicy{}, fmt.Errorf("decode retention: %w", err)
	}
	if decoded.RollbackDepth == nil {
		return RetentionPolicy{}, errors.New("retention is missing required field \"rollback_depth\"")
	}
	return RetentionPolicy{RollbackDepth: *decoded.RollbackDepth}, nil
}

func decodeRequiredV2Entries(raw json.RawMessage) ([]Entry, error) {
	if len(raw) == 0 {
		return nil, errors.New("missing required top-level field \"entries\"")
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("top-level field \"entries\" must be an array")
	}
	var encoded []json.RawMessage
	if err := decodeStrictJSON(raw, &encoded); err != nil {
		return nil, fmt.Errorf("decode entries: %w", err)
	}
	entries := make([]Entry, len(encoded))
	for i, value := range encoded {
		if err := requireV2EntryFields(value); err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		if err := decodeStrictJSON(value, &entries[i]); err != nil {
			return nil, fmt.Errorf("decode entry %d: %w", i, err)
		}
	}
	return entries, nil
}

func requireV2EntryFields(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("decode required fields: %w", err)
	}
	if fields == nil {
		return errors.New("entry must be an object")
	}
	for _, name := range []string{"active_activation_id", "activations", "update_policy"} {
		value, ok := fields[name]
		if !ok {
			return fmt.Errorf("missing required field %q", name)
		}
		if (name == "activations" || name == "update_policy") && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("required field %q must not be null", name)
		}
	}
	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

// Validate checks a normalized current-schema manifest without modifying it.
// It is the semantic boundary shared by Load, Save, and transaction encoders.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("nil manifest")
	}
	if m.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("manifest must be normalized to schema_version %d", CurrentSchemaVersion)
	}
	if m.Retention.RollbackDepth < 0 {
		return errors.New("negative manifest rollback depth")
	}
	names := make(map[string]struct{}, len(m.Entries))
	paths := make(map[string]struct{}, len(m.Entries))
	for _, entry := range m.Entries {
		if err := validateEntry(entry); err != nil {
			return fmt.Errorf("entry %q: %w", entry.Name, err)
		}
		if _, exists := names[entry.Name]; exists {
			return fmt.Errorf("duplicate entry name %q", entry.Name)
		}
		names[entry.Name] = struct{}{}
		cleanPath := filepath.Clean(entry.Path)
		if _, exists := paths[cleanPath]; exists {
			return fmt.Errorf("duplicate entry path %q", cleanPath)
		}
		paths[cleanPath] = struct{}{}
	}
	return nil
}

func validateEntry(entry Entry) error {
	if err := validateComponent("name", entry.Name, false); err != nil {
		return err
	}
	if entry.Path == "" || !filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path {
		return errors.New("path must be absolute and clean")
	}
	if err := ValidateActivationTag(entry.Tag); err != nil {
		return err
	}
	if !validDigest(entry.SHA256) {
		return errors.New("invalid entry SHA-256")
	}
	if err := ValidateRepository(entry.Repo); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, entry.AdoptedAt); err != nil {
		return fmt.Errorf("invalid adopted_at: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, entry.UpdatedAt); err != nil {
		return fmt.Errorf("invalid updated_at: %w", err)
	}
	if (entry.AssetName == "") != (entry.AssetSHA256 == "") {
		return errors.New("asset_name and asset_sha256 must be present together")
	}
	if entry.AssetSHA256 != "" && !validDigest(entry.AssetSHA256) {
		return errors.New("invalid asset SHA-256")
	}
	if entry.ChecksumVerified && (entry.AssetName == "" || entry.AssetSHA256 == "" || entry.ChecksumAsset == "") {
		return errors.New("checksum_verified requires complete asset and checksum evidence")
	}
	if entry.ChecksumAsset != "" && entry.AssetName == "" {
		return errors.New("checksum_asset requires asset evidence")
	}
	if entry.UpdatePolicy.Mode != UpdateModeSemver && entry.UpdatePolicy.Mode != UpdateModeLegacy {
		return fmt.Errorf("invalid update mode %q", entry.UpdatePolicy.Mode)
	}
	if entry.UpdatePolicy.Channel != UpdateChannelStable && entry.UpdatePolicy.Channel != UpdateChannelPrerelease {
		return fmt.Errorf("invalid update channel %q", entry.UpdatePolicy.Channel)
	}
	if entry.UpdatePolicy.PinnedTag != "" {
		if err := validateComponent("pinned tag", entry.UpdatePolicy.PinnedTag, false); err != nil {
			return err
		}
	}
	if entry.Retention != nil && entry.Retention.RollbackDepth < 0 {
		return errors.New("negative entry rollback depth")
	}
	return validateEntryLineage(entry)
}

func validateEntryLineage(entry Entry) error {
	if len(entry.Activations) == 0 || entry.ActiveActivationID == "" {
		return errors.New("missing active activation lineage")
	}
	seen := make(map[string]struct{}, len(entry.Activations))
	bindings := make(map[string]string, len(entry.Activations))
	for index, event := range entry.Activations {
		if err := validateDecodedActivation(event, bindings); err != nil {
			return fmt.Errorf("activation %d: %w", index, err)
		}
		if _, exists := seen[event.ID]; exists {
			return fmt.Errorf("duplicate activation id %q", event.ID)
		}
		if event.ParentID != "" {
			if _, exists := seen[event.ParentID]; !exists {
				return fmt.Errorf("activation %q has a missing or forward parent", event.ID)
			}
		}
		if event.RevertsID != "" {
			if _, exists := seen[event.RevertsID]; !exists {
				return fmt.Errorf("activation %q reverts a missing or forward event", event.ID)
			}
		}
		seen[event.ID] = struct{}{}
	}
	active := entry.Activations[len(entry.Activations)-1]
	if active.ID != entry.ActiveActivationID || active.Tag != entry.Tag || !strings.EqualFold(active.SHA256, entry.SHA256) {
		return errors.New("active activation does not match current tag and SHA-256")
	}
	return nil
}

func validateComponent(kind, value string, allowOriginal bool) error {
	if value == "" || value == "." || value == ".." || strings.Contains(value, "..") || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("invalid %s %q", kind, value)
	}
	if kind == "name" && strings.EqualFold(value, ".tmp") {
		return fmt.Errorf("invalid %s %q", kind, value)
	}
	if !allowOriginal && strings.EqualFold(value, "original") {
		return fmt.Errorf("invalid %s %q", kind, value)
	}
	return nil
}

// ValidateActivationTag verifies that a lineage tag is safe to use as one
// store directory component. The immutable backup namespace is available only
// through the exact lowercase spelling "original"; case variants remain
// reserved and are rejected with all other unsafe tag values.
func ValidateActivationTag(tag string) error {
	if tag == "original" {
		return nil
	}
	return validateComponent("activation tag", tag, false)
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ValidateRepository accepts an empty value for local entries; otherwise it
// requires the canonical owner/repo shape persisted by hukou.
func ValidateRepository(value string) error {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.TrimSpace(value) != value {
		return errors.New("repository must be owner/repo")
	}
	return nil
}

// validateDecodedV2 rejects incomplete or internally inconsistent lineage
// before Normalize can add compatibility defaults. In-memory callers may use
// PrepareEntry while constructing a new manifest, but a file that already
// claims schema v2 must carry the v2 safety fields explicitly.
func validateDecodedV2(m Manifest) error {
	if m.Retention.RollbackDepth < 0 {
		return errors.New("negative manifest rollback depth")
	}
	for _, entry := range m.Entries {
		if entry.UpdatePolicy.Mode != UpdateModeSemver && entry.UpdatePolicy.Mode != UpdateModeLegacy {
			return fmt.Errorf("entry %q has invalid update mode %q", entry.Name, entry.UpdatePolicy.Mode)
		}
		if entry.UpdatePolicy.Channel != UpdateChannelStable && entry.UpdatePolicy.Channel != UpdateChannelPrerelease {
			return fmt.Errorf("entry %q has invalid update channel %q", entry.Name, entry.UpdatePolicy.Channel)
		}
		if entry.Retention != nil && entry.Retention.RollbackDepth < 0 {
			return fmt.Errorf("entry %q has negative rollback depth", entry.Name)
		}
		if len(entry.Activations) == 0 || entry.ActiveActivationID == "" {
			return fmt.Errorf("entry %q has no active activation lineage", entry.Name)
		}
		seen := make(map[string]struct{}, len(entry.Activations))
		bindings := make(map[string]string, len(entry.Activations))
		for index, event := range entry.Activations {
			if err := validateDecodedActivation(event, bindings); err != nil {
				return fmt.Errorf("entry %q activation %d: %w", entry.Name, index, err)
			}
			if _, exists := seen[event.ID]; exists {
				return fmt.Errorf("entry %q has duplicate activation id %q", entry.Name, event.ID)
			}
			if event.ParentID != "" {
				if _, exists := seen[event.ParentID]; !exists {
					return fmt.Errorf("entry %q activation %q has a missing or forward parent", entry.Name, event.ID)
				}
			}
			if event.RevertsID != "" {
				if _, exists := seen[event.RevertsID]; !exists {
					return fmt.Errorf("entry %q activation %q reverts a missing or forward event", entry.Name, event.ID)
				}
			}
			seen[event.ID] = struct{}{}
		}
		active := entry.Activations[len(entry.Activations)-1]
		if active.ID != entry.ActiveActivationID || active.Tag != entry.Tag || !strings.EqualFold(active.SHA256, entry.SHA256) {
			return fmt.Errorf("entry %q active activation does not match current tag and SHA-256", entry.Name)
		}
	}
	return nil
}

func validateDecodedActivation(event ActivationEvent, bindings map[string]string) error {
	if event.ID == "" || len(event.ID) > 128 {
		return errors.New("invalid activation id")
	}
	for _, character := range event.ID {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return errors.New("activation id contains an unsupported character")
	}
	switch event.Operation {
	case "legacy", "adopt", "upgrade", "rollback", "repair":
	default:
		return fmt.Errorf("unsupported operation %q", event.Operation)
	}
	if err := ValidateActivationTag(event.Tag); err != nil {
		return err
	}
	if len(event.SHA256) != sha256.Size*2 {
		return errors.New("invalid activation SHA-256")
	}
	if _, err := hex.DecodeString(event.SHA256); err != nil {
		return errors.New("invalid activation SHA-256")
	}
	normalizedSHA := strings.ToLower(event.SHA256)
	if boundSHA, exists := bindings[event.Tag]; exists && boundSHA != normalizedSHA {
		return fmt.Errorf("activation tag %q is already bound to a different SHA-256", event.Tag)
	}
	bindings[event.Tag] = normalizedSHA
	if _, err := time.Parse(time.RFC3339, event.ActivatedAt); err != nil {
		return fmt.Errorf("invalid activation timestamp: %w", err)
	}
	return nil
}

// DefaultRetentionPolicy returns a fresh copy of the default retention
// settings so callers do not need to duplicate the current policy value.
func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{RollbackDepth: DefaultRollbackDepth}
}

// DefaultUpdatePolicy is used for newly-created schema-two entries.
func DefaultUpdatePolicy() UpdatePolicy {
	return UpdatePolicy{Mode: UpdateModeSemver, Channel: UpdateChannelStable}
}

// EffectiveRetention returns the per-entry override when present, otherwise
// the manifest-wide policy.
func (m *Manifest) EffectiveRetention(entry *Entry) RetentionPolicy {
	if entry != nil && entry.Retention != nil {
		return *entry.Retention
	}
	return m.Retention
}

// Normalize upgrades legacy schemas and fills deterministic schema-two
// defaults in place. Transaction callers should normalize the cloned
// after-manifest before encoding it so the journal payload and Save output are
// byte-for-byte identical.
func (m *Manifest) Normalize() error {
	if m == nil {
		return errors.New("nil manifest")
	}
	if m.SchemaVersion < 0 || m.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (current %d)", m.SchemaVersion, CurrentSchemaVersion)
	}
	if m.Entries == nil {
		m.Entries = make([]Entry, 0)
	}
	switch m.SchemaVersion {
	case 0, 1:
		migrateV1(m)
	case CurrentSchemaVersion:
		normalizeV2(m)
	}
	return nil
}

// Clone returns a deep copy suitable for constructing a transactional
// after-state. In particular, activation slices and per-entry retention
// pointers do not alias the source manifest.
func (m *Manifest) Clone() *Manifest {
	if m == nil {
		return nil
	}
	clone := *m
	clone.Entries = make([]Entry, len(m.Entries))
	for i, entry := range m.Entries {
		clone.Entries[i] = cloneEntry(entry)
	}
	// Build a fresh accelerator against the cloned Entries slice; sharing the
	// source map would alias positions across two independently mutable slices.
	clone.reindex()
	return &clone
}

func cloneEntry(entry Entry) Entry {
	clone := entry
	clone.Activations = append([]ActivationEvent(nil), entry.Activations...)
	if entry.Retention != nil {
		retention := *entry.Retention
		clone.Retention = &retention
	}
	return clone
}

// PrepareEntry deep-copies and normalizes a schema-two entry. A deterministic
// compatibility event is created only when legacy callers have changed the
// current tag/SHA without recording an explicit activation event. New v0.3
// command code should record adopt/upgrade/rollback through internal/activation
// before calling Put.
func PrepareEntry(entry Entry) Entry {
	prepared := cloneEntry(entry)
	if prepared.UpdatePolicy.Mode == "" {
		prepared.UpdatePolicy.Mode = UpdateModeSemver
	}
	if prepared.UpdatePolicy.Channel == "" {
		prepared.UpdatePolicy.Channel = UpdateChannelStable
	}
	if prepared.Tag == "" || prepared.SHA256 == "" {
		if prepared.Activations == nil {
			prepared.Activations = make([]ActivationEvent, 0)
		}
		return prepared
	}
	if len(prepared.Activations) == 0 && prepared.ActiveActivationID == "" {
		event := legacyActivation(prepared)
		prepared.Activations = []ActivationEvent{event}
		prepared.ActiveActivationID = event.ID
		return prepared
	}
	for _, event := range prepared.Activations {
		if event.ID != prepared.ActiveActivationID {
			continue
		}
		if event.Tag == prepared.Tag && strings.EqualFold(event.SHA256, prepared.SHA256) {
			return prepared
		}
		transition := legacyTransition(prepared, event.ID)
		prepared.Activations = append(prepared.Activations, transition)
		prepared.ActiveActivationID = transition.ID
		return prepared
	}
	return prepared
}

func migrateV1(m *Manifest) {
	m.SchemaVersion = CurrentSchemaVersion
	m.Retention = DefaultRetentionPolicy()
	for i := range m.Entries {
		entry := &m.Entries[i]
		entry.UpdatePolicy = UpdatePolicy{Mode: UpdateModeLegacy, Channel: UpdateChannelStable}
		entry.Retention = nil
		event := legacyActivation(*entry)
		entry.ActiveActivationID = event.ID
		entry.Activations = []ActivationEvent{event}
	}
}

func normalizeV2(m *Manifest) {
	// A manifest that already claims schema v2 must carry explicit policy and
	// activation fields. Defaults and synthetic history are migration tools for
	// schemas 0/1, not a way to guess missing v2 state.
}

func legacyActivation(entry Entry) ActivationEvent {
	activatedAt := entry.UpdatedAt
	if activatedAt == "" {
		activatedAt = entry.AdoptedAt
	}
	parts := []string{
		"hukou-manifest-v1-activation",
		entry.Name,
		entry.Path,
		entry.Repo,
		entry.Tag,
		entry.SHA256,
		entry.AdoptedAt,
		entry.UpdatedAt,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return ActivationEvent{
		ID:          fmt.Sprintf("legacy-%x", sum),
		Operation:   "legacy",
		Tag:         entry.Tag,
		SHA256:      entry.SHA256,
		ActivatedAt: activatedAt,
	}
}

func legacyTransition(entry Entry, parentID string) ActivationEvent {
	parts := []string{
		"hukou-schema-v2-compat-activation",
		entry.Name,
		parentID,
		entry.Tag,
		entry.SHA256,
		entry.UpdatedAt,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return ActivationEvent{
		ID:          fmt.Sprintf("legacy-%x", sum),
		ParentID:    parentID,
		Operation:   "legacy",
		Tag:         entry.Tag,
		SHA256:      strings.ToLower(entry.SHA256),
		ActivatedAt: entry.UpdatedAt,
	}
}

type durableOperations interface {
	AtomicWriteFile(path string, data []byte, mode os.FileMode) error
}

// Save preserves the previous decodable, supported-schema manifest as
// path+.bak, then writes the new manifest through a synced same-directory
// temporary file and persists the final rename. Existing manifest and backup
// paths must be regular files; a symlink or another file type fails closed.
func (m *Manifest) Save(path string) error {
	return m.save(path, durablefs.FileSystem{})
}

func (m *Manifest) save(path string, fs durableOperations) error {
	toWrite := m.Clone()
	if err := toWrite.Normalize(); err != nil {
		return err
	}
	if err := toWrite.Validate(); err != nil {
		return err
	}
	var encoded bytes.Buffer
	enc := json.NewEncoder(&encoded)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&toWrite); err != nil {
		return err
	}

	backupPath := path + ".bak"
	// Validate the backup namespace even on the first save. A pre-positioned
	// symlink or non-regular node must not be allowed to become latent state that
	// only breaks the next update.
	if err := requireRegularOrMissing(backupPath); err != nil {
		return fmt.Errorf("validate manifest backup: %w", err)
	}

	previous, previousInfo, err := readRegularFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read current manifest: %w", err)
	}
	if err == nil {
		if _, err := Decode(previous); err != nil {
			return fmt.Errorf("current manifest is not valid; refusing to replace it: %w", err)
		}
		if err := fs.AtomicWriteFile(backupPath, previous, 0o600); err != nil {
			return fmt.Errorf("preserve previous manifest: %w", err)
		}
		currentInfo, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("recheck current manifest: %w", statErr)
		}
		if !currentInfo.Mode().IsRegular() || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(previousInfo, currentInfo) {
			return fmt.Errorf("current manifest changed while saving: %s", path)
		}
	}

	if err := fs.AtomicWriteFile(path, encoded.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func readRegularFile(path string) ([]byte, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("manifest must not be a symlink: %s", path)
	}
	if !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("manifest is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, nil, fmt.Errorf("manifest changed while opening: %s", path)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	return data, before, nil
}

func requireRegularOrMissing(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("path must not be a symlink: %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("path is not a regular file: %s", path)
	}
	return nil
}

// buildIndex constructs the name->position accelerator for entries. It
// preserves the first-occurrence semantics of the slices.IndexFunc scan it
// accelerates, so a transiently duplicated name (rejected by Validate, but
// observable before it runs) resolves to the same entry it did before.
func buildIndex(entries []Entry) map[string]int {
	index := make(map[string]int, len(entries))
	for i := range entries {
		if _, ok := index[entries[i].Name]; !ok {
			index[entries[i].Name] = i
		}
	}
	return index
}

// reindex eagerly rebuilds the accelerator from the current Entries slice.
// Only construction (Load/Decode/Clone) and the mutating methods (Put/Remove)
// call it; read paths must stay write-free.
func (m *Manifest) reindex() {
	m.index = buildIndex(m.Entries)
}

// indexedHit returns the verified position of name, or -1 when the index
// cannot prove a hit. It is strictly read-only: a nil index (manifest literal,
// lenient json.Unmarshal) simply never hits, and a hit is trusted only after
// re-checking the referenced entry's Name against the live Entries slice, so
// external mutation of Entries can never surface a stale position.
func (m *Manifest) indexedHit(name string) int {
	idx, ok := m.index[name]
	if !ok || idx < 0 || idx >= len(m.Entries) || m.Entries[idx].Name != name {
		return -1
	}
	return idx
}

// locate returns the position of name using the verified index fast path and
// falling back to the authoritative linear scan on any miss.
func (m *Manifest) locate(name string) int {
	if idx := m.indexedHit(name); idx >= 0 {
		return idx
	}
	return slices.IndexFunc(m.Entries, func(e Entry) bool {
		return e.Name == name
	})
}

// Get returns the entry with the given name, or nil if not found.
// Get never mutates the manifest (including the internal index), so
// concurrent read-only lookups on a loaded manifest are safe.
func (m *Manifest) Get(name string) *Entry {
	idx := m.locate(name)
	if idx < 0 {
		return nil
	}
	return &m.Entries[idx]
}

// Put inserts or replaces an entry identified by Name.
// If an entry with the same Name exists it is replaced in place;
// otherwise the entry is appended. The internal index is rebuilt
// synchronously so subsequent reads stay lock-free.
func (m *Manifest) Put(entry Entry) {
	entry = cloneEntry(entry)
	idx := m.locate(entry.Name)
	if idx >= 0 {
		m.Entries[idx] = entry
	} else {
		m.Entries = append(m.Entries, entry)
	}
	m.reindex()
}

// Remove deletes the entry with the given name.
// It returns true if an entry was removed, false if it did not exist.
// Deletion shifts every later position, so the index is rebuilt eagerly;
// Remove is O(n), not O(1).
func (m *Manifest) Remove(name string) bool {
	idx := m.locate(name)
	if idx < 0 {
		return false
	}
	m.Entries = slices.Delete(m.Entries, idx, idx+1)
	m.reindex()
	return true
}
