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

## Return Path Safety

- The return capture source must never be the device we are writing to; that is
  a loop, not a return. `returnSource.checkNotFeedback` rejects it at startup.
- `-return-device` (a second virtual cable) and `-return-loopback` (WASAPI
  loopback of a playback device) are mutually exclusive and both opt-in.
  Loopback carries every system sound of that machine into the call — say so
  in any doc or log that mentions it.
- Device selection is fail-closed everywhere: empty selector, no match, and
  multiple matches are all errors.
- Only an explicit `a=recvonly` audio m-line enables the return track. Do not
  extend this to `a=sendrecv`: that m-line's receive direction is entangled
  with its send direction, and stopping it would kill the uplink too.
- When the peer asks to receive but no return track is configured, answer that
  m-line `inactive`. pion's default `sendonly` makes the page wait forever for
  audio that will never arrive.

## Control Plane Safety

- The receiver listens on nothing. It dials `-hub` outbound and authenticates
  with a Bearer token; `-monitor` is opt-in and never part of signaling.
- A pairing code is a short-lived capability, not identity. It is single-use,
  expires in 5 minutes, and is rate limited per client.
- Pairing failures must stay indistinguishable: unknown, expired, used and
  rate-limited codes all return `signaling.PairingFailedText`.
- Never log tokens, pairing codes, raw SDP, ICE candidates, or peer addresses.
- The hub only relays `offer` (sender to receiver) and `answer` (receiver to
  sender). Do not add a generic "forward to target" message.

## Testing

- Run pure Go selector tests: `go test -cover ./internal/receiverconfig ./internal/audiodevice`.
- Run control-plane tests: `go test -race -cover ./internal/signaling ./internal/hubclient`.
- Windows native builds need MSYS2 MinGW64 `gcc`, `pkg-config`, and `opus`, then
  use `go test -tags nolibopusfile ./cmd/receiver ./cmd/probe`.
- Build Windows commands with `-tags nolibopusfile`; the GitHub workflow is
  `.github/workflows/build-sender.yml`.

## Layout

- `cmd/signaling`: public control plane (HTTPS/WSS 443, pairing, SDP relay).
- `cmd/receiver`: dials the control plane, answers offers, writes to the virtual device.
- `cmd/probe`: device listing and virtual-cable tone check.
- `internal/audio`: device enumeration, playback, AGC, and recording.
- `internal/audiodevice`: driver-independent matching rules.
- `internal/hubclient`: the receiver's outbound WebSocket leg to the control plane.
- `internal/receiverconfig`: OS-specific receiver defaults.
- `internal/signaling`: pairing state machine, protocol, WSS server, config loading.

## Conventions

- Keep user-facing runtime errors actionable and Chinese, matching the existing
  command-line interface.
- Add a failing unit test before production behavior changes, then record the
  GREEN command in the change summary.
