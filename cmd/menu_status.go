package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/store"
)

// menuStatusAction renders the status overview screen: a read-only snapshot of
// every adopted tool with its active tag, live-file drift state, and retained
// version count.
//
// Snapshot semantics match `hukou doctor`: no mutation lock is taken and no
// network request is made; the table reflects whatever the manifest and store
// contained while it was being read. A manifest that fails to load prints a
// localized error and returns nil so the menu keeps running instead of
// bouncing the user out of the interactive loop.
func menuStatusAction(stdout, stderr io.Writer) error {
	return runMenuStatus(stdout, dataRoot())
}

// runMenuStatus is the testable core of menuStatusAction, rooted at an explicit
// data directory.
//
// The VERSIONS column counts retained versions via store.Versions — the same
// source as `hukou list`. This is deliberately not len(entry.Activations):
// the activation lineage records history, while retention pruning removes old
// versions from the store, so activations would overcount what is actually
// retained and rollback-able.
func runMenuStatus(stdout io.Writer, root string) error {
	m, err := manifest.Load(filepath.Join(root, "manifest.json"))
	if err != nil {
		fmt.Fprintln(stdout, i18n.T("error: %s", err))
		return nil
	}
	if len(m.Entries) == 0 {
		fmt.Fprintln(stdout, i18n.T("No tools have been adopted. Start with `hukou adopt <name|path>`."))
		return nil
	}

	entries := make([]manifest.Entry, len(m.Entries))
	copy(entries, m.Entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	s := &store.Store{Root: filepath.Join(root, "store")}
	t := output.NewTable(stdout,
		i18n.T("NAME"), i18n.T("TAG"), i18n.T("DRIFT"), i18n.T("VERSIONS"),
	)

	upToDate := 0
	drifted := 0
	for _, e := range entries {
		liveSHA, err := store.SHA256File(e.Path)
		if err != nil {
			return i18n.Wrapf("hash live file for %s: %w", err, e.Name)
		}
		state := i18n.T("ok")
		if !strings.EqualFold(liveSHA, e.SHA256) {
			state = i18n.T("DRIFTED")
			drifted++
		} else {
			upToDate++
		}
		versions, err := s.Versions(e.Name)
		if err != nil {
			return i18n.Wrapf("inspect store versions for %s: %w", err, e.Name)
		}
		t.Row(e.Name, e.Tag, state, strconv.Itoa(len(versions)))
	}
	if err := t.Flush(); err != nil {
		return i18n.Wrapf("render status table: %w", err)
	}

	fmt.Fprintln(stdout, i18n.T("%d adopted tool(s): %d up to date, %d drifted",
		len(entries), upToDate, drifted))
	return nil
}
