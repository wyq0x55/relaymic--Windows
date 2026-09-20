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
| Playback and capture share one name-normalization rule | `go test ./internal/audiodevice` failed: `NormalizeName` undefined | `go test -cover ./internal/audiodevice ./internal/receiverconfig` passed, both 100.0% statements |

## Additional verification

- `go vet ./internal/receiverconfig ./internal/audiodevice` passed.
- `git diff --check origin/master...HEAD` passed.
- A focused secrets scan over the changed files found no matches.
- With the native CGO toolchain configured, `go test -tags nolibopusfile ./...`
  and `go vet -tags nolibopusfile ./...` both passed.

## Native build and runtime evidence

With `CGO_ENABLED=1`, Scoop MinGW64 GCC, pkgconf, and a static Opus library
built from the official source, the following passed:

```powershell
go test -tags nolibopusfile ./cmd/receiver ./cmd/probe
```

Static `relaymic-receiver.exe` (29.1 MB) and `relaymic-probe.exe` (7.5 MB)
were also built successfully. The probe and receiver were run without a cable;
each listed the available Realtek/display playback devices and exited with the
actionable missing-`cable input` error. The receiver exit code was 1, before it
started listening or opened a physical playback device.

This machine has no VB-CABLE endpoint, so the evidence proves Windows native
build and fail-closed behavior but not VB-CABLE routing or Teams execution.
