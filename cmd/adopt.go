package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/lookpath"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/spf13/cobra"
)

var (
	adoptLocal  bool
	adoptForce  bool
	adoptTag    string
	adoptDryRun bool
	adoptJSON   bool
)

var adoptCmd = &cobra.Command{
	Use:   "adopt <name|path> [owner/repo]",
	Short: "Adopt an unmanaged executable into hukou",
	Long: `adopt registers one existing executable. A bare name is resolved on
PATH; a path is used directly. hukou may infer github.com/owner/repo from Go
build information. Otherwise provide owner/repo explicitly or use --local.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runAdopt,
}

func init() {
	adoptCmd.Flags().BoolVar(&adoptLocal, "local", false, "adopt without a release repository; the default tag is local")
	adoptCmd.Flags().BoolVar(&adoptForce, "force", false, "override another manager's ownership claim")
	adoptCmd.Flags().StringVar(&adoptTag, "tag", "", "record this version tag")
	adoptCmd.Flags().BoolVar(&adoptDryRun, "dry-run", false, "inspect adoption without writing or recovering state")
	adoptCmd.Flags().BoolVar(&adoptJSON, "json", false, "emit a JSON plan (requires --dry-run)")
	rootCmd.AddCommand(adoptCmd)
}

func runAdopt(cmd *cobra.Command, args []string) error {
	target := args[0]
	var repoArg string
	if len(args) > 1 {
		repoArg = args[1]
	}
	if adoptJSON && !adoptDryRun {
		return fail(fmt.Errorf("--json requires --dry-run"))
	}
	if adoptDryRun {
		return doAdoptDryRun(cmd.OutOrStdout(), target, repoArg, adoptLocal, adoptTag, adoptForce, adoptJSON, runSecurityGate)
	}
	return doAdopt(cmd.OutOrStdout(), cmd.ErrOrStderr(), target, repoArg, adoptLocal, adoptTag, adoptForce)
}

func doAdopt(stdout, stderr io.Writer, target, repoArg string, local bool, tag string, force bool) error {
	return doAdoptWithDeps(stdout, stderr, target, repoArg, local, tag, force, runSecurityGate, saveManifest)
}

type adoptionInspection struct {
	plan     output.AdoptPlan
	entry    manifest.Entry
	manifest *manifest.Manifest
	planMode os.FileMode
}

func doAdoptDryRun(stdout io.Writer, target, repoArg string, local bool, tag string, force, jsonOutput bool, securityGate func(string) (*provenance.Attribution, error)) error {
	if err := ensureDryRunTransactionClean(); err != nil {
		return fail(err)
	}
	inspection, err := inspectAdoption(target, repoArg, local, tag, force, securityGate)
	if err != nil {
		return fail(err)
	}
	if jsonOutput {
		return fail(output.WriteAdoptPlanJSON(stdout, inspection.plan))
	}
	return fail(output.WriteAdoptPlan(stdout, inspection.plan))
}

func doAdoptWithDeps(stdout, stderr io.Writer, target, repoArg string, local bool, tag string, force bool, securityGate func(string) (*provenance.Attribution, error), save func(*manifest.Manifest) error) error {
	lock, err := acquireMutationLock(stderr)
	if err != nil {
		return fail(fmt.Errorf("acquire state lock: %w", err))
	}
	defer releaseMutationLock(lock, stderr)

	// A real adoption never trusts a prior dry-run. Resolve the target and
	// repeat every validation while holding the mutation lock.
	inspection, err := inspectAdoption(target, repoArg, local, tag, force, securityGate)
	if err != nil {
		return fail(err)
	}
	entry := inspection.entry
	eventID, err := activation.NewID()
	if err != nil {
		return fail(fmt.Errorf("prepare adoption activation: %w", err))
	}
	if err := activation.RecordAdopt(&entry, eventID, entry.UpdatedAt); err != nil {
		return fail(fmt.Errorf("prepare adoption activation: %w", err))
	}
	m := inspection.manifest
	binPath := entry.Path
	name := entry.Name
	sha := entry.SHA256
	tag = entry.Tag
	repo := entry.Repo

	s := newStore()
	if err := s.GC(); err != nil {
		return fail(err)
	}

	if _, err := s.PreflightOriginalPath(name, filepath.Base(binPath)); err != nil {
		return fail(fmt.Errorf("preflight original backup: %w", err))
	}
	origPath, err := s.PrepareOriginalPath(name, filepath.Base(binPath))
	if err != nil {
		return fail(fmt.Errorf("prepare original path: %w", err))
	}
	if _, err := os.Lstat(origPath); err == nil {
		return fail(fmt.Errorf("original backup already exists: %s", origPath))
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	originalSource := binPath
	if liveTopology, err := os.Lstat(binPath); err != nil {
		return fail(fmt.Errorf("inspect live path before adoption: %w", err))
	} else if liveTopology.Mode()&os.ModeSymlink != 0 {
		originalSource, err = filepath.EvalSymlinks(binPath)
		if err != nil {
			return fail(fmt.Errorf("resolve live symlink before adoption: %w", err))
		}
	}

	afterManifest := m.Clone()
	afterManifest.Put(entry)
	if err := afterManifest.Normalize(); err != nil {
		return fail(fmt.Errorf("normalize adoption manifest: %w", err))
	}
	manifestBytes, err := encodeManifest(afterManifest)
	if err != nil {
		return fail(fmt.Errorf("encode adoption manifest: %w", err))
	}
	tx, err := statejournal.Begin(dataRoot(), "adopt", name, []statejournal.Spec{
		{Role: "original", Path: origPath, After: statejournal.RegularFile(originalSource)},
		{Role: "live-guard", Path: binPath, After: statejournal.Unchanged()},
		{Role: "manifest", Path: manifestPath(), After: statejournal.RegularBytes(manifestBytes, 0o600)},
	})
	if err != nil {
		return fail(fmt.Errorf("prepare adoption transaction: %w", err))
	}
	beforeOriginal, _ := tx.Before("original")
	if beforeOriginal.Kind != statejournal.KindAbsent {
		return fail(abortStateTransaction(tx, fmt.Errorf("original backup appeared concurrently: %s", origPath)))
	}
	if err := validateTransactionStateSHA(tx, "original", sha, true); err != nil {
		return fail(abortStateTransaction(tx, err))
	}
	if err := validateTransactionStateSHA(tx, "live-guard", sha, false); err != nil {
		return fail(abortStateTransaction(tx, fmt.Errorf("binary changed while preparing adoption: %w", err)))
	}
	if err := tx.Apply("original"); err != nil {
		return fail(abortStateTransaction(tx, fmt.Errorf("backup original: %w", err)))
	}
	originalEntries, entriesErr := os.ReadDir(filepath.Dir(origPath))
	if entriesErr != nil || len(originalEntries) != 1 || originalEntries[0].Name() != filepath.Base(origPath) || !originalEntries[0].Type().IsRegular() {
		return fail(abortStateTransaction(tx, fmt.Errorf("original backup namespace changed during adoption; refusing inconsistent adoption: %v", entriesErr)))
	}
	backupSHA, backupErr := store.SHA256File(origPath)
	liveSHA, liveErr := store.SHA256File(binPath)
	backupInfo, backupStatErr := os.Stat(origPath)
	liveInfo, liveStatErr := os.Stat(binPath)
	modeChanged := backupStatErr == nil && liveStatErr == nil &&
		(backupInfo.Mode().Perm() != inspection.planMode || liveInfo.Mode().Perm() != inspection.planMode)
	if backupErr != nil || liveErr != nil || backupStatErr != nil || liveStatErr != nil || backupSHA != sha || liveSHA != sha || modeChanged {
		return fail(abortStateTransaction(tx, fmt.Errorf("binary changed while creating original backup; refusing inconsistent adoption (backup_err=%v live_err=%v backup_stat_err=%v live_stat_err=%v)", backupErr, liveErr, backupStatErr, liveStatErr)))
	}

	if err := tx.Verify("manifest", false); err != nil {
		return fail(abortStateTransaction(tx, fmt.Errorf("manifest changed during adoption; refusing overwrite: %w", err)))
	}
	m.Put(entry)
	if err := save(m); err != nil {
		m.Remove(name)
		return fail(abortStateTransaction(tx, fmt.Errorf("save manifest: %w", err)))
	}
	if err := commitStateTransaction(tx, stderr); err != nil {
		return fail(fmt.Errorf("commit adoption transaction: %w", err))
	}
	finalizeStateTransaction(tx, stderr, name, "adoption")

	fmt.Fprintf(stdout, "Adopted %s (%s) at %s\n", name, tag, binPath)
	if repo != "" {
		fmt.Fprintf(stdout, "repo: %s\n", repo)
	}
	return nil
}

func inspectAdoption(target, repoArg string, local bool, tag string, force bool, securityGate func(string) (*provenance.Attribution, error)) (*adoptionInspection, error) {
	binPath, err := resolveAdoptTarget(target)
	if err != nil {
		return nil, fmt.Errorf("locate target: %w", err)
	}
	info, err := os.Stat(binPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", binPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", binPath)
	}
	if info.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("%s is not executable", binPath)
	}
	if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, fmt.Errorf("%s uses privileged/special mode bits that hukou does not preserve", binPath)
	}

	name := filepath.Base(binPath)
	if err := store.ValidateName(name); err != nil {
		return nil, fmt.Errorf("invalid tool name: %w", err)
	}
	sha, err := store.SHA256File(binPath)
	if err != nil {
		return nil, fmt.Errorf("sha256 %s: %w", binPath, err)
	}

	var repo, upstream string
	if local {
		repo = ""
		if tag == "" {
			tag = "local"
		}
	} else {
		if repoArg != "" {
			repo = repoArg
		} else {
			if goInfo, ok := provenance.ReadGoBinary(binPath); ok {
				upstream = goInfo.ModulePath
				repo = modulePathToRepo(goInfo.ModulePath)
				if tag == "" && goInfo.Version != "" && goInfo.Version != "(devel)" {
					tag = goInfo.Version
				}
			}
		}
		if repo == "" {
			return nil, fmt.Errorf("cannot infer a repository; provide owner/repo or use --local")
		}
		if tag == "" {
			tag = "adopted"
		}
	}
	if err := manifest.ValidateRepository(repo); err != nil {
		return nil, fmt.Errorf("invalid repository: %w", err)
	}
	if err := store.ValidateTag(tag); err != nil {
		return nil, fmt.Errorf("invalid adoption tag: %w", err)
	}

	attr, err := securityGate(binPath)
	if err != nil {
		return nil, err
	}
	if attr == nil {
		return nil, fmt.Errorf("the ownership safety check returned no result")
	}
	if !allowedAdoptSource(attr.Source) && !force {
		return nil, fmt.Errorf("%s is owned by %s (%s); use --force to override explicitly", name, attr.Source, attr.Evidence)
	}

	m, err := loadManifest()
	if err != nil {
		return nil, err
	}
	if existing := m.Get(name); existing != nil {
		return nil, fmt.Errorf("%s is already adopted at %s; refusing to replace the entry", name, existing.Path)
	}
	cleanPath := filepath.Clean(binPath)
	for _, existing := range m.Entries {
		if filepath.Clean(existing.Path) == cleanPath {
			return nil, fmt.Errorf("path %s is already registered as %s; refusing duplicate adoption", binPath, existing.Name)
		}
	}

	s := newStore()
	plannedOriginal, err := s.PreflightOriginalPath(name, filepath.Base(binPath))
	if err != nil {
		return nil, fmt.Errorf("preflight original backup: %w", err)
	}

	now := rfc3339Now()
	entry := manifest.Entry{
		Name:      name,
		Path:      binPath,
		Repo:      repo,
		Tag:       tag,
		SHA256:    sha,
		Upstream:  upstream,
		AdoptedAt: now,
		UpdatedAt: now,
	}
	return &adoptionInspection{
		plan: output.AdoptPlan{
			SchemaVersion: 1,
			Name:          name,
			Path:          binPath,
			SHA256:        sha,
			Repo:          repo,
			Tag:           tag,
			Local:         local,
			Attribution: output.AdoptAttribution{
				Source:     attr.Source,
				Package:    attr.Package,
				Version:    attr.Version,
				Upstream:   attr.Upstream,
				Confidence: attr.Confidence,
				Evidence:   attr.Evidence,
			},
			PlannedWrites: []string{
				dataRoot(),
				filepath.Join(dataRoot(), "state.lock"),
				filepath.Join(storeRoot(), ".tmp"),
				plannedOriginal,
				manifestPath(),
				filepath.Join(dataRoot(), "transactions"),
			},
		},
		entry:    entry,
		manifest: m,
		planMode: info.Mode().Perm(),
	}, nil
}

func resolveAdoptTarget(target string) (string, error) {
	if filepath.IsAbs(target) || strings.ContainsAny(target, `/\`) {
		return filepath.Abs(target)
	}
	return lookpath.LookPath(target)
}
