# Project Instructions

## Stack

- Go module: `github.com/hueshu/relaymic`; `go.mod` requires Go 1.26.6.
- Native audio: `malgo`/miniaudio and `hraban/opus`; commands need CGO.
- Web sender: `internal/web`; native commands live in `cmd/`.

## Receiver Safety

- Keep virtual-device selection explicit. Do not fall back to the system
  playback device when the requested virtual device is missing or ambiguous.
- Windows defaults to the VB-CABLE playback endpoint `CABLE Input`; Teams reads
  the matching recording endpoint `CABLE Output`.
- Preserve the 150 ms jitter buffer and AGC unless the task explicitly changes
  audio-processing behavior.

## Testing

- Run pure Go selector tests: `go test -cover ./internal/receiverconfig ./internal/audiodevice`.
- Windows native builds need MSYS2 MinGW64 `gcc`, `pkg-config`, and `opus`, then
  use `go test -tags nolibopusfile ./cmd/receiver ./cmd/probe`.
- Build Windows commands with `-tags nolibopusfile`; the GitHub workflow is
  `.github/workflows/build-sender.yml`.

## Layout

- `cmd/receiver`: WebRTC receiver and virtual playback output.
- `cmd/probe`: device listing and virtual-cable tone check.
- `internal/audio`: device enumeration, playback, AGC, and recording.
- `internal/audiodevice`: driver-independent matching rules.
- `internal/receiverconfig`: OS-specific receiver defaults.

## Conventions

- Keep user-facing runtime errors actionable and Chinese, matching the existing
  command-line interface.
- Add a failing unit test before production behavior changes, then record the
  GREEN command in the change summary.
