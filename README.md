# RelayMic

> **English** · [中文](README.zh-CN.md)

**Turn the microphone in any device's browser into a system input device on a remote Mac.**

Remote desktop forwards your screen, your keyboard and your mouse — not your voice. Windows has
had microphone redirection since RDP shipped it. But the moment the machine on the other end is a
Mac, that feature is gone from every tool on the market. Not hidden in a menu. Not there.

RelayMic fills that gap.

```
The device in front of you (any browser)
  ↓  captures the mic → Opus 48 kHz stereo
  ↓  encrypted WebRTC (direct when possible, TURN relay when not)
The remote Mac
  ↓  decode → jitter buffer → write into a virtual audio device
Zoom / dictation / Audacity / anything — reads it as an ordinary microphone
```

It does **not** replace your remote desktop tool. It runs alongside TeamViewer, AnyDesk, Parsec,
RustDesk, Jump Desktop, ToDesk — those keep doing screen and input, unaware anything changed.

## Who can forward your microphone to a Mac

| Tool | Screen / input | Your microphone |
|---|---|---|
| TeamViewer | ✅ | ❌ picks up the Mac's own mic instead |
| AnyDesk | ✅ | ❌ |
| Parsec | ✅ | ❌ |
| RustDesk | ✅ | ❌ |
| Jump Desktop | ✅ | ❌ |
| ToDesk | ✅ | ❌ has mic mapping, but the controlled end must be Windows |
| macOS Screen Sharing | ✅ | ❌ |
| Microsoft RDP *(far end is Windows)* | ✅ | ✅ built in — and it stops at Windows |
| **RelayMic** | leaves that to your remote tool | ✅ as a system input device |

The pattern is simple: **when the controlled machine runs Windows, your mic usually gets through;
when it runs macOS, nothing does.**

## Install

**Hand this repository to your AI assistant and tell it to read [`SETUP.md`](SETUP.md).**

Claude Code, Codex, Cursor — any of them. It installs the dependencies, builds, gets it running,
and then teaches you how to use it. You don't type the commands yourself.

```
Clone it, then tell your AI:
"Follow SETUP.md to install RelayMic, then teach me how to use it."
```

`SETUP.md` is written for an AI to execute: every step has a verification, every failure has a
troubleshooting entry.

Doing it by hand works too — that document reads fine for humans. Roughly: put both machines on
Tailscale → on the Mac, `brew install opus && brew install --cask blackhole-2ch` →
`go build -tags nolibopusfile ./cmd/receiver` → run it → open
`https://<mac's tailnet IP>:7420` in a browser on the other device.

## What this is, honestly

**This is the author's own tool, opened up — not a polished consumer product.**

- Command line, no GUI, no installer
- UI strings and logs are currently in Chinese
- **You need to set up a network first.** There is no public signalling server in this version,
  so the sending browser must reach the Mac's port `7420` directly. In practice that means
  **Tailscale** (free — both ends get a stable `100.x.x.x`, port 7420 is directly reachable, and
  WebRTC connects inside that virtual network). Consumer broadband in many countries sits behind
  carrier-grade NAT with no public IP at all, where port forwarding and DDNS cannot help
- The input device on the Mac shows up as `BlackHole 2ch`, not "RelayMic"

But **the audio path itself has been in daily use** — three Macs, every day. The parameters below
are what they are because something broke without them, and "optimizing" them is not advised:

- **150 ms jitter buffer** — measured, not guessed. 20 ms sounds brilliant on a LAN and stutters
  the first time you use hotel Wi-Fi
- **Silence suppression (DTX) off by default** — it saves bandwidth by clipping the front of
  quietly spoken words, and dictation loses the first syllable
- **Three diagnostic recording taps** — before processing, after the buffer, and read back out of
  the virtual device. "It sounded bad" becomes a waveform you can point at

Latency is roughly a phone call: network round trip plus that 150 ms buffer. Built for talking,
dictating and meetings; not for monitoring yourself while recording music.

## You might not need it

- **An iPhone in the same room as the Mac** → Apple's Continuity Microphone is free
- **Windows to Windows** → RDP already redirects your mic, free
- RelayMic is for **distance**: different building, different city, different country

## Privacy

Audio travels over an encrypted peer-to-peer connection. When it can connect directly, nothing
passes through a third party at all; when two networks refuse, it falls back to a TURN relay —
**one you configure and control**.

**No server of the author's is in the path, so there is nothing on our side that could listen.**
The code is here — read it. That is part of why a tool that handles your voice should be open
source: "we don't store it" is worth less than being able to check.

## Layout

```
cmd/receiver     Mac receiver: takes the stream, decodes, writes to the virtual device,
                 and serves the web sender
cmd/sender       command-line sender
cmd/sender-gui   Windows GUI sender (frozen — the web sender covers it)
cmd/probe, selfcheck, stuncheck, turncheck   diagnostics
internal/audio   audio devices and processing chain
internal/rtc     WebRTC send/receive
internal/sender  sender engine
internal/web     web sender (embedded in the binary)
site/            relaymic.com landing page (Cloudflare Workers)
docs/            design and decision records
```

## Not planned

Each of these was considered and rejected; writing them down saves the discussion:

- **New features in the native Windows sender.** The web sender covers the same ground;
  `cmd/sender-gui` is frozen where it is
- **Windows → Windows.** Microsoft's RDP redirects the microphone already, free and better
- **Same-room iPhone → Mac as the main use case.** Apple's Continuity Microphone is free
- **Changing the measured audio parameters.** Buffer depth, DTX, the gain gate, stretch
  compensation — each one came out of a specific failure

## Build

```bash
brew install opus
go build -tags nolibopusfile ./...
go test  -tags nolibopusfile ./internal/...
```

`-tags nolibopusfile` is required — the project only uses the codec, never reads `.opus` files,
and without the tag the build tries to link libopusfile and fails.

## License

[AGPL-3.0](LICENSE).

You may run, study, modify and share it, including commercially. But if you distribute a modified
version **or let people use one over a network**, you owe those people the complete source of
your version under the same license — running a modified copy on a server counts, even without
handing out a binary.

If you need to build something closed-source on top of this, the copyright is mine to license
differently: <hey@relaymic.com>.

BlackHole is a separate open-source project (MIT). RelayMic points you at its official Homebrew
package rather than bundling a copy.
