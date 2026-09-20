# Windows receiver TDD evidence

## Source and journeys

Derived from Issue #1: route decoded RelayMic audio to VB-CABLE on Windows
without falling back to physical speakers.

1. A Windows receiver defaults to the VB-CABLE playback endpoint while macOS
   and other existing targets retain the BlackHole selector.
2. A receiver stops instead of choosing an arbitrary device when the selector
   is empty, missing, or matches more than one playback endpoint.

## RED and GREEN evidence

| Guarantee | RED command and result | GREEN command and result |
| --- | --- | --- |
| Windows default is `cable input`; other systems keep `blackhole` | `go test ./internal/receiverconfig` failed: `DefaultOutputDeviceForOS` undefined, then failed because Windows returned `cable` | `go test -cover ./internal/receiverconfig` passed, 100.0% statements |
| Device matching accepts exactly one normalized match and rejects empty, absent, and ambiguous selectors | `go test ./internal/audiodevice` failed: `UniqueMatchIndex` undefined | `go test -cover ./internal/audiodevice` passed, 100.0% statements |

## Additional verification

- `go vet ./internal/receiverconfig ./internal/audiodevice` passed.
- `git diff --check origin/master...HEAD` passed.
- A focused secrets scan over the changed files found no matches.

## Known validation boundary

The full Receiver/probe CGO build was attempted with:

```powershell
go test -tags nolibopusfile ./cmd/receiver ./cmd/probe
```

It did not compile because this machine has `CGO_ENABLED=0` and lacks the
MSYS2 MinGW64 GCC, pkg-config, and Opus dependencies. MSYS2 installation
completed, but its first-run GPG key refresh timed out three times, so those
packages were not installed. The machine also has no VB-CABLE endpoint (only
Realtek audio was detected). Consequently this document is source-level test
evidence, not proof of a Windows VB-CABLE or Teams execution.
