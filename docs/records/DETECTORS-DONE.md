# DETECTORS DONE

## Results

- `go build ./... && go vet ./... && go test ./...`: passed
- `go build -o ./bin/hukou .`: passed
- `./bin/hukou scan --json | python3 -m json.tool`: passed
- `./bin/hukou scan`: passed
- Scan summary on this machine: total=1898 sources=8 unknown=22 shadowed=10 skipped=12
- Source breakdown: brew=567 curl-installer=4 npm=19 pnpm=3 rustup=14 system=1264 unknown=22 uv=5

## Implemented Tier 1 detectors

- internal/provenance/brew.go
- internal/provenance/macports.go
- internal/provenance/cargo.go
- internal/provenance/rustup.go
- internal/provenance/go.go
- internal/provenance/npm.go
- internal/provenance/pnpm.go
- internal/provenance/yarn.go
- internal/provenance/bun.go
- internal/provenance/pipx.go
- internal/provenance/uv.go
- internal/provenance/pip_user.go
- internal/provenance/mise.go
- internal/provenance/asdf.go
- internal/provenance/gem.go
- internal/provenance/nix.go
- internal/provenance/volta.go
- internal/provenance/deno.go
- internal/provenance/dotnet.go
- internal/provenance/composer.go
- internal/provenance/krew.go
- internal/provenance/curl_installer.go

## Other changed files

- internal/provenance/env.go
- internal/provenance/runner.go
- internal/provenance/helpers.go
- internal/provenance/detectors_test.go
