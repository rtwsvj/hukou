package scan

// BinKind classifies an executable by its file header / content.
type BinKind string

const (
	KindMachO  BinKind = "MachO"
	KindELF    BinKind = "ELF"
	KindScript BinKind = "Script"
	KindOther  BinKind = "Other"
)

// Binary is a PATH-discovered executable.
type Binary struct {
	Name     string  // base name
	Path     string  // path as found on PATH (may be a symlink)
	RealPath string  // EvalSymlinks result; empty if evaluation failed
	Kind     BinKind // MachO | ELF | Script | Other
	Shadowed bool    // true if a same-named binary earlier on PATH takes precedence
}
