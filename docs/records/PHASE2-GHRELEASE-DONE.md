# Phase 2 ghrelease done

Implemented `internal/ghrelease` GitHub release client and httptest coverage.

## Files

- `internal/ghrelease/client.go`
- `internal/ghrelease/client_test.go`

## Coverage

- latest release success
- by-tag release success
- 404 status error
- 429 retry twice then success with 1s/2s backoff
- 5xx retry exhaustion with 1s/2s/4s backoff
- 403 rate-limit error including `X-RateLimit-Reset`
- download streams to a temporary file under `destDir` without creating the final asset name

## Validation

`go build ./... && go vet ./... && go test ./internal/ghrelease/`

Result: green

```text
ok  	github.com/rtwsvj/hukou/internal/ghrelease	(cached)
```
