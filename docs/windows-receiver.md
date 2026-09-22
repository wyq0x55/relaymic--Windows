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

RelayMic never falls back to the Windows default speakers when the configured virtual cable cannot be found. Device selection must succeed before the receiver starts; an empty or ambiguous selector also exits and lists the matching endpoints. This prevents remote microphone audio from leaking through physical speakers.

## Local console

Start the Receiver with `-monitor "127.0.0.1:7420"`, then open
`http://127.0.0.1:7420/monitor` on the same Windows computer. It shows the current
one-time pairing code and countdown, ICE path, live audio level, packet statistics,
and the selected devices. The Receiver token is never displayed.

The settings form scans the machine (`GET /api/devices`, loopback only) and offers the
devices in a dropdown, grouped into `虚拟线` and `其他设备`: writing into the virtual
microphone and capturing a second virtual cable are the two paths that matter, and picking
a physical device there does not fail — it just silently produces no audio. A value saved
as a substring (`CABLE Input`) is matched to the scanned full name, and a device that is
currently missing stays visible as the current value instead of being silently replaced.
"重新扫描设备" picks up a cable installed while the page is open.

The pairing code is intentionally hidden when the monitor is opened through a LAN address;
use the loopback URL above to view or copy it.

## Two-way audio and public networks

Use a second virtual cable for Teams speaker return: Teams plays to `VoiceMeeter Aux Input`,
and start the Receiver with `-return-device "VoiceMeeter Aux Output"`. Public Signaling and
short-lived coturn credentials are documented in `docs/signaling.md`; use `-force-relay` only
when validating the relay path.

Running `relaymic` (or `relaymic receiver`) with no console arguments defaults to
`-monitor 127.0.0.1:7420 -open`, so the page comes up on its own. `-monitor ""` turns the
console off and `-open=false` only skips the browser.
