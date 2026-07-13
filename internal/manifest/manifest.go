package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// Load reads a manifest file. If the file does not exist an empty
// manifest with SchemaVersion=1 is returned (no error). Any JSON
// decode error or unknown schema_version is returned as an error.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Manifest{SchemaVersion: schemaVersion, Entries: make([]Entry, 0)}, nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if m.SchemaVersion == 0 {
		m.SchemaVersion = schemaVersion
	}
	if m.SchemaVersion > schemaVersion {
		return nil, fmt.Errorf("unsupported schema_version %d (current %d)", m.SchemaVersion, schemaVersion)
	}
	if m.Entries == nil {
		m.Entries = make([]Entry, 0)
	}
	return &m, nil
}

// Save writes the manifest atomically: write to a temporary file in the
// same directory, then os.Rename to the destination.
func (m *Manifest) Save(path string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "manifest-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Ensure the temp file is removed if anything goes wrong.
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename: %w", err)
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
