package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/rtwsvj/hukou/internal/durablefs"
)

const schemaVersion = 1

// Entry is a single record in the manifest.
// All time fields are RFC3339 strings supplied by the caller; the library
// never calls time.Now or any time function.
type Entry struct {
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

// Manifest is the top-level document stored as JSON.
type Manifest struct {
	SchemaVersion int     `json:"schema_version"`
	Entries       []Entry `json:"entries"`
}

// Load reads a regular manifest file without following a symlink. If the file
// does not exist an empty manifest with SchemaVersion=1 is returned (no error).
// Any JSON decode error or unknown schema_version is returned as an error.
func Load(path string) (*Manifest, error) {
	data, _, err := readRegularFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Manifest{SchemaVersion: schemaVersion, Entries: make([]Entry, 0)}, nil
		}
		return nil, err
	}
	return decode(data)
}

func decode(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if m.SchemaVersion == 0 {
		m.SchemaVersion = schemaVersion
	}
	if m.SchemaVersion < 0 || m.SchemaVersion > schemaVersion {
		return nil, fmt.Errorf("unsupported schema_version %d (current %d)", m.SchemaVersion, schemaVersion)
	}
	if m.Entries == nil {
		m.Entries = make([]Entry, 0)
	}
	return &m, nil
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
	if m.SchemaVersion < 0 || m.SchemaVersion > schemaVersion {
		return fmt.Errorf("unsupported schema_version %d (current %d)", m.SchemaVersion, schemaVersion)
	}
	toWrite := *m
	if toWrite.SchemaVersion == 0 {
		toWrite.SchemaVersion = schemaVersion
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
		if _, err := decode(previous); err != nil {
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

// Get returns the entry with the given name, or nil if not found.
func (m *Manifest) Get(name string) *Entry {
	idx := slices.IndexFunc(m.Entries, func(e Entry) bool {
		return e.Name == name
	})
	if idx < 0 {
		return nil
	}
	return &m.Entries[idx]
}

// Put inserts or replaces an entry identified by Name.
// If an entry with the same Name exists it is replaced in place;
// otherwise the entry is appended.
func (m *Manifest) Put(entry Entry) {
	idx := slices.IndexFunc(m.Entries, func(e Entry) bool {
		return e.Name == entry.Name
	})
	if idx >= 0 {
		m.Entries[idx] = entry
		return
	}
	m.Entries = append(m.Entries, entry)
}

// Remove deletes the entry with the given name.
// It returns true if an entry was removed, false if it did not exist.
func (m *Manifest) Remove(name string) bool {
	idx := slices.IndexFunc(m.Entries, func(e Entry) bool {
		return e.Name == name
	})
	if idx < 0 {
		return false
	}
	m.Entries = slices.Delete(m.Entries, idx, idx+1)
	return true
}
