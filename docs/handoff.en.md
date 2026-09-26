# Hand RelayMic to Someone Else

> English · [简体中文](handoff.zh-CN.md)

The handoff is **one executable, one receiver token, and one Hub address**. The recipient needs a Windows PC and a browser; they do not need Go, network changes, or an inbound firewall rule.

Replace `<Hub host>` below with your own domain or IP. Do not put real server addresses or tokens in published copies of this guide.

## 1. Prepare four items

| Item | Where to get it | Notes |
| --- | --- | --- |
| `relaymic.exe` | GitHub Actions `relaymic-windows` artifact, or build with `go build -tags nolibopusfile -ldflags "-extldflags -static" -o relaymic.exe ./cmd/relaymic` | About 28 MB; statically linked, so the recipient needs no runtime installation |
| Receiver token | `relaymic signaling -gen-receiver "recipient name"` | Generate a separate token for each receiver |
| Hub URL | Your deployed control plane, e.g. `wss://<Hub host>/ws/receiver` | |
| Sender page | `https://<Hub host>/` | Open on the phone or computer that will send audio |

**Recommended: generate the token in the Hub admin page** (see the admin section in [`signaling.en.md`](signaling.en.md)). Open `https://<Hub host>/admin`, sign in, enter a name, and select **Generate token**. Send the one-time token to the recipient through a secure channel. They save it as `receiver-token.txt`. **The Hub does not need a restart, and active calls stay connected.**

If the admin page is not enabled, generate the receiver entry on the VPS:

```bash
relaymic signaling -gen-receiver "recipient PC"
```

The command prints JSON containing the receiver name and token. Add that entry to the Hub's `receivers` configuration, then restart the Hub. A restart disconnects all current sessions; active callers will need to pair again.

## 2. Set up the recipient's PC

### Install virtual audio devices

RelayMic does not install audio drivers. Install a virtual cable and reboot if its installer requests it.

- Required microphone path: RelayMic → `CABLE Input` (playback) → `CABLE Output` (recording) → meeting app microphone.
- Optional return-audio path: meeting app speaker → `VoiceMeeter Aux Input` (playback) → `VoiceMeeter Aux Output` (recording) → RelayMic.

VB-CABLE is enough for one-way microphone audio. Add Voicemeeter AUX VAIO for two-way audio.

### Copy the portable folder

```text
C:\RelayMic\relaymic.exe
C:\RelayMic\config.json
C:\RelayMic\receiver-token.txt
```

The executable automatically uses `config.json` and `receiver-token.txt` beside itself. Relative token paths are resolved from the configuration file's directory, so the folder can be moved to another drive or PC.

Before delivery, create `config.json` and replace the Hub host. Remove `returnDevice` if return audio is not enabled. Do not put the token itself in this JSON file:

```json
{
  "hub": "wss://<Hub host>/ws/receiver",
  "tokenFile": "receiver-token.txt",
  "device": "CABLE Input",
  "returnDevice": "VoiceMeeter Aux Output"
}
```

If you omit `config.json`, the recipient can still open the local console and enter the Hub and device settings there. `receiver-token.txt` beside the executable is still discovered automatically.

### Start RelayMic

Run:

```text
C:\RelayMic\relaymic.exe
```

With no arguments, RelayMic starts the receiver, opens a local console at `127.0.0.1:7420`, and launches the default browser. Windows SmartScreen may warn because the executable is unsigned; the recipient can select **More info → Run anyway** after verifying the file came from you.

### Check the console and meeting-app devices

- Confirm the Hub URL, output device, and optional return device. Select `CABLE Input (VB-Audio Virtual Cable)` for output; the console labels virtual devices.
- Select **Save and restart receiver**. Device lists are scanned live; after installing a cable, select **Rescan devices**.
- In the meeting app, choose `CABLE Output (VB-Audio Virtual Cable)` as the microphone.
- For return audio, choose `VoiceMeeter Aux Input` as the meeting-app speaker.

### Start a call

1. Read the six-digit pairing code in the local console. It expires after five minutes and is replaced automatically.
2. Share the code with the sender.
3. On the sender's device, open `https://<Hub host>/`, enter the code, and allow microphone access.

## 3. Day-to-day use and troubleshooting

- Start `relaymic.exe` when the PC boots; add it to Startup if it should run continuously.
- If `config.json` is beside the executable, console settings are saved there. Otherwise they are saved to `%USERPROFILE%\.config\relaymic\config.json`. Command-line flags override file settings. The config survives executable updates.
- On networks that only allow an HTTP proxy, configure the proxy (`-proxy` or `HTTPS_PROXY`) and enable `turnTunnel` / `-turn-tunnel`. The proxy carries the Hub connection; the tunnel carries TURN/TCP through the Hub. `-turn-tunnel` alone does not fix a blocked WebSocket connection.

| Symptom | Check |
| --- | --- |
| Output device is ambiguous | Select the full device name in the console (for example, distinguish `CABLE In 16ch` from `CABLE Input`). |
| Device is busy | Close the other app using the cable or select another cable. |
| Sender cannot hear the receiver | Check the meeting app's microphone is `CABLE Output` and RelayMic writes to `CABLE Input`. |
| Receiver cannot hear meeting audio | Enable return audio and choose the second virtual cable as the meeting-app speaker. |
| Console says it is waiting for the Hub | Check the Hub URL, token, and outbound network access. |
| Stuck connecting to the control plane | Check Hub URL, credentials, proxy, and HTTPS/WebSocket egress. TURN tunneling only affects TURN; it does not repair the Hub connection. |
| Sender page does not open | Check that the Hub is running and its TLS certificate is valid. |

## 4. Security and limitations

- A token is a credential. Use one per person/device and never post it in a group. Revoke it in the admin page, or remove its entry from the Hub config and restart the Hub if the admin page is disabled.
- The Hub handles pairing and relays signaling messages; audio uses a direct peer connection when possible, then your configured TURN relay if needed.
- Use a publicly trusted TLS certificate and verify automatic renewal. If it expires, receivers cannot connect.
- RelayMic is not a stealth tool and does not hide that two devices are involved or bypass company IT or endpoint auditing. Make sure everyone involved understands the setup.

## Related guides

- Hub and TURN deployment: [`vps-signaling-turn-agent-guide.en.md`](vps-signaling-turn-agent-guide.en.md) and [`signaling.en.md`](signaling.en.md)
- Windows audio path and console: [`windows-receiver.md`](windows-receiver.md)
- 中文版：[`handoff.zh-CN.md`](handoff.zh-CN.md)
