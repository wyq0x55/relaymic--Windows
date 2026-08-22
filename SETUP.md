# RelayMic — Installation Guide (for an AI assistant to execute)

> **English** · [中文](SETUP.zh-CN.md)

> **Human reader**: hand this file, along with the repository, to your AI assistant (Claude Code,
> Codex, Cursor, …) and tell it to install this. When it's done it will teach you how to use it.
> You do not need to type any of the commands below yourself.

---

## Instructions for the AI

You are installing RelayMic for a user. **Work through this in order.** Every step has a
verification — **if a verification fails, stop and diagnose; do not carry on to the next step.**
When everything is installed, jump to the last section, "What to teach the user afterwards".

This is an early project: a command-line tool, not a packaged app. Installation requires
compiling. If the user asks why it's this involved, tell them plainly: the author open-sourced a
tool they built for themselves and never made an installer.

**Language**: the logs and UI strings in this repository are currently in Chinese. If the user
does not read Chinese, you will need to translate what's on screen when you walk them through it.

---

## What it is

It turns the microphone in any device's browser into a system input device on a remote Mac.

Remote desktop software (TeamViewer, AnyDesk, Parsec, RustDesk, Jump Desktop, ToDesk, macOS
Screen Sharing) forwards the screen, keyboard and mouse — **not your microphone**. Windows RDP
has microphone redirection, but it does not exist when the far end is a Mac. RelayMic fills that
gap.

Data flow:

```
The device in front of the user (any browser)
  ↓ captures the mic → Opus 48 kHz stereo
  ↓ encrypted WebRTC (direct when possible, TURN relay when not)
relaymic receiver on the remote Mac
  ↓ decode → jitter buffer → write into the BlackHole virtual audio device
Any app on the Mac (Zoom / dictation / Audacity …) reads BlackHole as an ordinary mic
```

RelayMic does **not** replace the remote desktop tool — it runs alongside whichever one the user
already has.

---

## Prerequisites

| Item | Requirement | Check |
|---|---|---|
| Remote Mac | macOS 12+ | `sw_vers -productVersion` |
| Homebrew | installed | `brew --version` |
| Go | 1.26+ | `go version` |
| Sending side | any browser with a microphone | — |

### Network reachability — **solve this first or the rest is wasted**

RelayMic needs two paths, and both must work:

1. **Signalling**: the sending browser must reach `https://<mac>:7420` to fetch the page and
   exchange SDP
2. **Media**: the WebRTC audio stream — direct when possible, TURN relay when not

The first one is the one people miss. **It requires the Mac's port 7420 to be reachable from the
sending device** — which is genuinely hard across the public internet. This version ships no
public signalling server (that is unfinished productisation work), so it has to be solved with
networking.

Three options, most viable first:

| Option | Signalling | Media | Viability on consumer broadband |
|---|---|---|---|
| **Tailscale** (recommended) | ✅ direct inside the tailnet | ✅ already direct in the virtual network | ✅ free, works right after install |
| Public IP + port forwarding + DDNS | ⚠️ needs a stable or dynamic hostname | needs STUN, possibly TURN | ❌ impossible behind carrier-grade NAT |
| Same LAN | ✅ | ✅ | ✅ but this case usually doesn't need RelayMic |

**Default to Tailscale.** It's a WireGuard overlay: both machines get a stable `100.x.x.x`
address, port 7420 is directly reachable, and WebRTC connects inside that virtual network — TURN
is rarely needed at all. This is also how the author runs it; the auto-discovery in
`internal/discover` is built around it.

```bash
# install on both machines
brew install --cask tailscale        # Mac
# other platforms: https://tailscale.com/download
```

Have the user sign in on both ends with the same account, then:

```bash
tailscale ip -4        # the Mac's tailnet IP, of the form 100.x.x.x
```

**Verify**: ping that address from the sending device. Only continue once it responds.

> **If both machines really are on the same LAN**, a `192.168.x.x` address works and Tailscale
> isn't needed. But think first — if they're in the same room, Apple's Continuity Microphone
> already connects an iPhone to a Mac for free.

Installing Go and Homebrew if missing:

```bash
# Homebrew
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Go
brew install go
```

---

## Step 1: audio dependencies

**Run these on the remote Mac** — the one that should receive the sound.

```bash
brew install opus
brew install --cask blackhole-2ch
```

- `opus` is the audio codec library, linked at build time through cgo.
- `blackhole-2ch` is an open-source virtual audio device (MIT). RelayMic writes decoded audio
  into it, and other apps on the Mac read from it. **RelayMic does not bundle or redistribute
  it** — Homebrew installs the official package.

**Installing BlackHole triggers a system prompt** — it's an audio driver and needs the user's
approval. Have them click Allow under `System Settings → Privacy & Security`. A restart (or at
least a log out and back in) may be required afterwards.

**Verify**:

```bash
system_profiler SPAudioDataType | grep -i blackhole
```

`BlackHole 2ch` must appear. If it doesn't, the driver hasn't loaded — have the user restart and
try again. **Do not continue past a failure here**; everything downstream depends on it.

---

## Step 2: build

```bash
cd <repository directory>
go build -tags nolibopusfile -o bin/relaymic-receiver ./cmd/receiver
```

`-tags nolibopusfile` is required: without it the build looks for libopusfile (we only use the
codec, never read `.opus` files) and fails to link on a machine that only has `opus`.

**Verify**:

```bash
./bin/relaymic-receiver -h
```

Printing the flag list means it worked.

If the build fails with something like `opus/opus.h: No such file`, cgo can't find Homebrew's
headers. On Apple Silicon, try:

```bash
export CGO_CFLAGS="-I$(brew --prefix)/include"
export CGO_LDFLAGS="-L$(brew --prefix)/lib"
```

then rebuild.

---

## Step 3: first run

```bash
./bin/relaymic-receiver
```

A normal start prints something like:

```
监听 :7420
发送端地址: https://localhost:7420
发送端地址: https://192.168.1.23:7420
```

(Those Chinese lines mean "listening on :7420" and "sender address".)

**Pick the address the sending device will use**: the `100.x.x.x` one if Tailscale is set up,
otherwise `192.168.x.x` on the same LAN.

Two things that look like faults but aren't:

- **The Mac speaks briefly at startup.** Deliberate: on macOS, BlackHole falls back to an
  inactive state when unused, and the first open by a non-Apple process hangs. The code runs the
  system `say` command first to wake it. This was learned the hard way — don't "optimize" it away.
- **Failure to open the device is retried in-process**, without exiting. Also deliberate —
  exiting and restarting leaves an unkillable remnant inside coreaudiod, and each cycle adds
  another one.

**Verify**, in a second terminal:

```bash
curl -sk https://localhost:7420/api/status
```

JSON back means the service is alive. **Verify through the API, not by checking whether the
process exists** — a live process doesn't mean a working audio path.

---

## Step 4: connect the sender

On the **other device** (the one in front of the user — Windows, iPad, another Mac), open the
Mac's address with port `7420`:

- via Tailscale: `https://100.x.x.x:7420`
- same LAN: `https://192.168.x.x:7420`

**The browser will warn that the certificate isn't trusted** — expected. RelayMic uses a
self-signed certificate; a LAN tool can't get a publicly trusted one. Have the user click through:

- Chrome/Edge: `Advanced` → `Proceed to … (unsafe)`
- Safari: `Show Details` → `visit this website`

> **Why HTTPS is mandatory**: browsers only grant microphone access in a secure context. An
> `http://` page cannot call `getUserMedia`. The self-signed certificate isn't laziness, it's a
> hard requirement.

Once the page loads:

1. The browser asks for microphone permission → have the user allow it
2. Click the button labelled 「开始说话」 ("start talking")
3. The status changes from 「未连接」 ("not connected") and the level meter starts moving

---

## Step 5: receive the sound on the Mac

Back on the remote Mac. The audio is now inside BlackHole, and any app that takes BlackHole as
its input will hear it.

**Tell the user**: in whichever app needs the microphone, set the input device to
**`BlackHole 2ch`**.

- Zoom: `Settings → Audio → Microphone` → `BlackHole 2ch`
- System dictation: `System Settings → Sound → Input` → `BlackHole 2ch`
- Audacity / OBS / anything else: in that app's own audio input setting

> **Note**: the device is listed as **`BlackHole 2ch`**, not "RelayMic". That's how this version
> works.

**Verify the whole chain**:

```bash
./bin/relaymic-receiver -meter
```

`-meter` prints the incoming level once a second. Have the user talk into the sending device;
the bar should move. Movement means audio really arrived.

There is also a monitor page at `https://<mac-ip>:7420/monitor` with waveforms and statistics.

---

## Common flags

```bash
./bin/relaymic-receiver \
  -addr :7420 \           # listen address
  -device blackhole \     # output device name (substring match)
  -buffer 150 \           # jitter buffer depth, milliseconds
  -meter                  # print levels, for diagnosis
```

**Don't casually lower `-buffer 150`.** 150 ms was measured on real networks. 20 ms sounds
great on a LAN and falls apart on hotel Wi-Fi. Likewise `-dtx` is off by default because silence
suppression clips the front of quietly spoken words and dictation loses the first syllable.

**Across the public internet** you need a TURN relay for when NAT traversal fails:

```bash
./bin/relaymic-receiver \
  -turn turn:your-turn-server:3478 \
  -turn-user <username> \
  -turn-pass <password>
```

Without your own TURN server, use it only where a direct connection is possible — LAN, Tailscale,
VPN. Cloudflare Realtime TURN has a free tier if the user wants one.

---

## Running in the background (optional)

If the user wants it to survive closing the terminal, use launchd. Write
`~/Library/LaunchAgents/com.relaymic.receiver.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.relaymic.receiver</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/<username>/relaymic/bin/relaymic-receiver</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/relaymic.log</string>
  <key>StandardErrorPath</key><string>/tmp/relaymic.err</string>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.relaymic.receiver.plist
```

**When updating the binary, `rm` it before `cp` — don't overwrite in place.** Overwriting a
running executable lets macOS mix old and new, and the symptom is bizarre behaviour at startup.

---

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| Build can't find `opus.h` | cgo can't see Homebrew's headers — set `CGO_CFLAGS` / `CGO_LDFLAGS`, see Step 2 |
| Startup reports 「音频初始化失败」 (audio init failed) | BlackHole isn't installed or approved. Go back to Step 1 and verify with `system_profiler` |
| Startup hangs | BlackHole is in its inactive state. Wake it manually with `say -a "BlackHole 2ch" " "` and start again |
| No microphone permission prompt in the browser | The page isn't HTTPS, or the user denied it earlier. Reset the permission in the browser's site settings |
| The page won't load at all | Port 7420 isn't reachable. This is a signalling problem, not a WebRTC one — check both ends are online with `tailscale status` |
| Page loads but won't connect | Signalling works, media doesn't. Rare inside Tailscale; across the public internet you need TURN |
| Connected but the Mac hears nothing | Wrong input device in the app. It must be `BlackHole 2ch` |
| Choppy audio | Network jitter. Raise `-buffer` to 200–300 |
| Dictation drops the first syllable | Don't enable `-dtx` (it's off by default) |
| Device becomes permanently unopenable after repeated restarts | Remnants inside coreaudiod: `sudo killall coreaudiod` (briefly interrupts system audio) |

Diagnostics (under `cmd/`):

```bash
go run -tags nolibopusfile ./cmd/selfcheck    # environment self-check
go run -tags nolibopusfile ./cmd/stuncheck    # STUN reachability
go run -tags nolibopusfile ./cmd/turncheck    # TURN reachability
go run -tags nolibopusfile ./cmd/probe        # audio device probe
```

---

## What to teach the user afterwards

Don't just say "it's installed". Cover these, in the user's own language:

1. **How to start it day to day** — which command, or that launchd already starts it at login
2. **What the sender address is** — that `https://<ip>:7420`; suggest bookmarking it
3. **The certificate warning is normal** — every new device has to click through once
4. **Select `BlackHole 2ch` in the app** — the step people get stuck on. Be explicit that the
   device is not called RelayMic
5. **Use different dictation shortcuts on the two machines** — if the local machine and the
   remote Mac both trigger dictation on the same key, one press fires both. Have the user change
   the remote Mac's shortcut to something else
6. **Roughly how much latency** — network round trip plus a 150 ms buffer, close to a phone call.
   Fine for talking, dictation and meetings; **not** for monitoring yourself while recording
7. **Privacy** — the audio goes over an encrypted peer-to-peer connection and doesn't touch a
   third party when it connects directly. No server of the author's is involved, so there is
   nothing on that side that could record. The code is open and can be checked

If the user's scenario is "an iPhone in the same room as the Mac", tell them they **don't need
RelayMic** — Apple's Continuity Microphone does that for free. RelayMic is about distance:
different building, different city, different country.

---

## Project status (tell the user honestly)

- This is the open-source version of the author's own tool, **not a polished consumer product**
- Command line, no GUI, no installer
- UI strings and logs are currently in Chinese
- The "six-digit pairing code" and "one-click installer" described on relaymic.com **do not exist
  yet** — that was unfinished productisation work
- But **the audio path itself is battle-tested**: three of the author's Macs use it daily, and the
  buffer depth, silence suppression and gain strategy are all tuned from real failures

Licensed under AGPL-3.0. Issues and questions on GitHub.
