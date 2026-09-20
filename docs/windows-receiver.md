# Windows receiver

RelayMic can use an installed virtual audio cable as the Windows microphone bridge. The receiver does **not** install or emulate a kernel audio driver.

## Audio path

```
Browser microphone
  -> WebRTC / Opus
  -> RelayMic receiver
  -> Windows playback endpoint: CABLE Input
  -> VB-CABLE
  -> Windows recording endpoint: CABLE Output
  -> Microsoft Teams microphone
```

The existing `internal/audio` path uses miniaudio through `malgo`; on Windows this selects the native Windows audio backend. The receiver therefore only needs to render decoded PCM to the VB-CABLE playback endpoint.

## Setup

1. Install VB-CABLE (or another Windows virtual cable).
2. Reboot if the driver installer requests it.
3. Build RelayMic on Windows.
4. Run `receiver.exe`. On Windows the default `-device` selector is `CABLE Input`.
5. Confirm the startup log says `虚拟麦克风输出设备: CABLE Input ...`.
6. In Teams, choose the corresponding **CABLE Output** recording device as the microphone.

If multiple devices contain `cable`, pass a more specific selector:

```powershell
.\receiver.exe -device "CABLE Input"
```

Before connecting a browser, verify the cable path with the bundled probe. It
lists playback devices and writes a 440 Hz tone only to the selected virtual
cable; Teams should show activity on `CABLE Output` while the tone plays.

```powershell
go run -tags nolibopusfile ./cmd/probe -device "CABLE Input" -tone 3s
```

## Safety / failure behavior

RelayMic never falls back to the Windows default speakers when the configured virtual cable cannot be found. Device selection must succeed before the receiver starts. This prevents remote microphone audio from leaking through physical speakers.

## Current milestone boundary

This milestone is microphone-only. Teams speaker audio is not routed back through RelayMic. Public signaling and TURN are tracked separately in #2 and #3.
