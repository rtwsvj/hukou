# hukou demo tape — recorded with charmbracelet/vhs (https://github.com/charmbracelet/vhs)
#
# Render:   make build && vhs scripts/demo.tape
# Produces: assets/demo.gif
#
# The visible session runs entirely inside a disposable sandbox: an isolated
# PATH, a temporary HUKOU_DATA_DIR, and a throwaway "rg" binary. Nothing on the
# real machine is scanned, adopted, or modified. The `upgrade` step downloads a
# real ripgrep release from GitHub, so rendering needs network access; the
# `-> 15.2.0` target simply tracks whatever ripgrep's latest release is.

Output assets/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1300
Set Height 720
Set Padding 24
Set TypingSpeed 45ms
Set PlaybackSpeed 1.0

# --- hidden setup: build, sandbox, isolated PATH, disposable binary ----------
Hide
Type `go build -o "$PWD/bin/hukou" .` Enter
Sleep 4s
Type `export HK="$PWD/bin/hukou"` Enter
Type `hukou() { "$HK" "$@"; }` Enter
Type `export SANDBOX="$(mktemp -d)"` Enter
Type `export HUKOU_DATA_DIR="$SANDBOX/data"` Enter
Type `mkdir -p "$SANDBOX/bin"` Enter
Type `printf '#!/bin/sh\necho "rg 13.0.0"\n' > "$SANDBOX/bin/rg" && chmod 755 "$SANDBOX/bin/rg"` Enter
Type `export PATH="$SANDBOX/bin:/usr/bin:/bin:/usr/sbin:/sbin"` Enter
Type `export PS1='$ '` Enter
Type "clear" Enter
Show

# --- 1. who owns what on PATH? (local, read-only, no network) ----------------
Type "hukou scan --unknown-only" Sleep 600ms Enter
Sleep 3s

# --- 2. adopt a stray, tied to its GitHub repo (original bytes preserved) -----
Type "hukou adopt rg BurntSushi/ripgrep --tag 13.0.0" Sleep 600ms Enter
Sleep 2500ms

# --- 3. preview the upgrade — --dry-run never touches the file ----------------
Type "hukou upgrade rg --dry-run" Sleep 600ms Enter
Sleep 3s

# --- 4. apply it: checksum-verified download + atomic replace ----------------
Type "hukou upgrade rg" Sleep 600ms Enter
Sleep 7s

# --- 5. retained versions are visible ----------------------------------------
Type "hukou list" Sleep 600ms Enter
Sleep 2500ms

# --- 6. roll back to the previous version ------------------------------------
Type "hukou rollback rg" Sleep 600ms Enter
Sleep 3s

Sleep 2s
