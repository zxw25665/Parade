$ErrorActionPreference = "Stop"

$env:SKIP_MDNS_TEST = "1"

Write-Host "=== Parade cluster and backend integration tests ==="
Write-Host "Mode: in-process Go sync, libp2p wire, and app tests"

go test -count=1 -timeout=180s ./internal/core/sync/...
go test -count=1 -timeout=180s ./internal/network/...
go test -count=1 -timeout=180s ./internal/app/...

Write-Host "All integration test packages passed."
