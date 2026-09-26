# Public Signaling (#2)

> English · [简体中文](signaling.md)

RelayMic used to require the sender to reach the receiver directly on port `7420`. That does not work when the receiver is behind a corporate firewall or NAT. The current design replaces that requirement: both peers connect out to the same Hub.

## Topology

```
Sender device                     Public server                 Receiver PC
Browser -- HTTPS/WSS 443 ------> relaymic-signaling <------ WSS 443 -- Receiver
   |                                      |
   |         SDP signaling only           |  Audio does not pass through Hub
   +---------------- WebRTC audio, P2P preferred --------------------->
                      TURN relay if direct connectivity fails
```

- The Hub's HTTPS/WSS endpoint is the only public signaling entry point.
- The receiver has no public inbound listener. It dials out to the Hub, so no inbound firewall rule is needed for the receiver.
- Tailscale is not required for this signaling path.
- The Hub forwards signaling messages, not audio. WebRTC connects peers directly when possible and uses TURN when configured and needed.

## User flow

1. Start the receiver and connect it to the Hub:

   ```powershell
   relaymic.exe receiver -hub "wss://mic.example.com/ws/receiver" `
     -token-file "C:\relaymic\receiver-token.txt" `
     -monitor "127.0.0.1:7420"
   ```

   On the receiver PC, open `http://127.0.0.1:7420/monitor` to view the pairing code, expiry countdown, connection path, and packet statistics. The code expires after five minutes and is replaced automatically while the receiver is idle.

2. On the sender device, open `https://mic.example.com`, enter the code, grant microphone access, and start speaking.
3. On Windows, audio is written to VB-CABLE's `CABLE Input`; select `CABLE Output` as the microphone in Teams or another app.

## Protocol

The Hub exposes one WebSocket channel with a fixed message set. It is not a general-purpose forwarding proxy.

| Direction | Type | Purpose |
| --- | --- | --- |
| Hub → Receiver | `waiting` | Pairing code and time remaining; sends a replacement after expiry |
| Hub → Receiver | `joined` | Sender claimed the code; includes a session ID |
| Hub → Receiver | `left` | Sender left; a fresh code follows |
| Hub → Sender | `paired` | Pairing succeeded; includes session ID and receiver name |
| Hub → Sender | `closed` | Receiver went offline; the session cannot continue |
| Hub → either peer | `error` | User-displayable error, such as a pairing failure |
| Sender → Hub | `pair` | Claim a receiver with a pairing code |
| Sender → Hub → Receiver | `offer` | Forward the complete SDP offer unchanged |
| Receiver → Hub → Sender | `answer` | Forward the complete SDP answer unchanged |
| Receiver → Hub | `close` | End the current session; Hub disconnects the sender and issues a new code |

SDP is carried as `json.RawMessage`: the Hub does not parse, rewrite, or cache it. Each message is limited to 64 KiB; larger messages are disconnected.

`GET /api/ice` serves public STUN configuration over HTTPS. Only after pairing does the Hub send short-lived TURN credentials to both peers over their existing WebSocket connections. The public endpoint never returns TURN passwords.

## Security model

A six-digit pairing code is a short-lived pairing capability, not an identity credential.

| Surface | Protection |
| --- | --- |
| Receiver identity | A separate 256-bit bearer token per receiver; Hub stores only its SHA-256 digest |
| Pairing code | Uniformly generated with `crypto/rand`, expires after five minutes, consumed on use; one active code per receiver, automatically replaced on expiry |
| Guessing | Five failed attempts per client per minute; a connection is closed after three failures |
| Failure responses | Unknown, expired, used, and rate-limited codes return the same message to prevent enumeration |
| Browser origin | Validates `Origin`; same-origin is the default, and `allowedOrigins` can restrict it further |
| Forwarding | Only sender→`offer` and receiver→`answer` are relayed; other message types close the connection |
| TURN | Hub reads the coturn REST shared secret from a restricted file; creates ten-minute, session-scoped HMAC credentials and sends them only after pairing |
| Logs | Do not record tokens, pairing codes, raw SDP, ICE candidates, or peer IP addresses |

When a receiver reconnects, its previous connection is closed immediately so two sessions cannot own the same receiver. Closing the old connection does not invalidate the new connection's pairing code.

## Deployment

### Optional admin page

The admin page is disabled by default. Without it, adding a receiver requires editing the Hub config and restarting the Hub, which disconnects active calls. Set both `adminTokenFile` and `receiversFile` to enable `/admin`:

```bash
sudo sh -c 'umask 077; relaymic-signaling -gen-admin-token > /etc/relaymic/admin-token'
sudo chown relaymic:relaymic /etc/relaymic/admin-token
```

```json
{
  "adminTokenFile": "/etc/relaymic/admin-token",
  "receiversFile": "/var/lib/relaymic/receivers.json"
}
```

- Open `https://<Hub host>/admin`, sign in, generate receiver tokens, view connected receivers, or revoke one. The password is stored in an HttpOnly cookie, inaccessible to page scripts.
- Scripts can use a bearer token: `curl -H "Authorization: Bearer <password>" https://<Hub host>/admin/api/receivers`.
- `receiversFile` stores token digests, not usable plaintext credentials.
- The service account must be able to write to the `receiversFile` directory. With systemd `ProtectSystem=strict`, `/var/lib` is read-only by default; set `StateDirectory=relaymic` or use an already writable directory. Otherwise token creation fails with a read-only filesystem error.
- If the directory is not writable, the Hub logs a warning but can still start; the admin page returns HTTP 500 with a reason when token creation is attempted.
- A new token is shown only once. Save it as `receiver-token.txt`; the Hub does not need to restart.
- Revocation takes effect immediately and disconnects that receiver.
- Treat the admin password like a root credential. The login endpoint is rate-limited to eight failures per minute; do not reuse passwords or put them in shell history.

When the admin page is enabled, `receivers` may be empty; add receivers through the page.

### Generate receiver credentials

```bash
relaymic-signaling -gen-receiver "office PC"
```

Generate several entries by repeating the flag:

```bash
relaymic-signaling -gen-receiver "Alice-PC" -gen-receiver "Bob-PC"
```

The output is a JSON array. A token is shown only once: store each token in that receiver's `-token-file`. File permissions protect the credential; command-line arguments may be visible in process listings. Tokens and sessions are isolated per receiver, so multiple receivers can be in calls at the same time.

An online, idle receiver always has an active code. The Hub replaces it after five minutes; it does not issue a new code during a call. Senders therefore do not need the receiver to reconnect if they miss an expired code.

### Hub configuration

```json
{
  "listen": ":443",
  "certFile": "/etc/letsencrypt/live/mic.example.com/fullchain.pem",
  "keyFile": "/etc/letsencrypt/live/mic.example.com/privkey.pem",
  "allowedOrigins": ["mic.example.com"],
  "iceServers": [{"urls": ["stun:stun.l.google.com:19302"]}],
  "turn": {
    "urls": ["turn:turn.example.com:3478?transport=udp"],
    "authSecretFile": "/etc/relaymic/turn-auth-secret",
    "credentialTTLSeconds": 600
  },
  "receivers": [{"name": "office PC", "token": "<32-byte raw URL-safe base64>"}]
}
```

Unknown config keys cause startup to fail. `iceServers` accepts only STUN servers without credentials; static TURN URLs or passwords are rejected to prevent leaking relay credentials from `/api/ice`.

### Start the Hub

```bash
relaymic-signaling -config /etc/relaymic/signaling.json
```

Example systemd unit:

```ini
[Unit]
Description=RelayMic signaling
After=network-online.target

[Service]
ExecStart=/usr/local/bin/relaymic-signaling -config /etc/relaymic/signaling.json
Restart=always
RestartSec=2
User=relaymic

[Install]
WantedBy=multi-user.target
```

The control plane is stateless: pairing codes and sessions live in memory and are invalidated on restart. Receivers reconnect and get new codes automatically.

### Reverse proxy (optional)

If Nginx or Caddy is in front of the Hub, preserve the `Origin` and `Authorization` headers and forward `Upgrade` and `Connection` for `/ws/`. The proxy must enforce rate limits: the Hub uses the direct client address for `clientKey` and does not trust `X-Forwarded-For`.

## Receiver options

| Flag | Description |
| --- | --- |
| `-hub` | Required Hub URL, e.g. `wss://mic.example.com/ws/receiver` |
| `-token` / `-token-file` | Receiver credential; use one or the other |
| `-monitor` | Local console bind address, e.g. `127.0.0.1:7420`; empty disables the console listener |
| `-device` | Virtual audio device; Windows defaults to `CABLE Input` |

When the Hub has a `turn` config, the receiver applies the short-lived ICE config after pairing. Do not pass `-turn-user` or `-turn-pass` on the command line. Use `-force-relay` only to verify the coturn path.

The receiver has no public inbound listener. The pairing code is returned only for requests to `127.0.0.1` or `::1`; requests through other addresses show it as hidden so it is not exposed to the LAN.

## Local smoke test

```powershell
relaymic-signaling -config signaling.json -addr 127.0.0.1:8099 -plain
relaymic.exe receiver -hub ws://127.0.0.1:8099/ws/receiver -token-file token.txt -device "CABLE Input"
```

Use `-plain` only for local testing. Browsers grant microphone access only in a secure context, so the real sender page must use HTTPS. `-self-signed-dir <directory>` can be used to test the TLS path with a self-signed certificate.

## Limitations

- coturn must use REST API authentication (`use-auth-secret` / `static-auth-secret`) and share the same secret as `turn.authSecretFile`. The Hub does not install or manage coturn.
- After deployment, run a real two-peer call with `-force-relay`. Do not claim symmetric-NAT support until this succeeds.
- Pairing codes are one-time: after a call ends or a receiver reconnects, the sender must enter a fresh code.
- `cmd/sender` and `cmd/selfcheck` still use the retired LAN protocol and target the removed `https://<host>:7420/offer`; `cmd/sender-gui` has been removed. The browser page is the only supported sender for the current signaling flow; the status of the old commands and `internal/discover` needs a separate decision.

## HTTP-proxy-only networks: TURN tunnel

Some corporate networks block direct egress, DNS, STUN, and TURN over UDP/TCP, allowing only HTTPS/WSS through an HTTP proxy. A TURN client does not use an HTTP proxy directly, so TURN cannot allocate a relay address in that network.

The Hub can expose a tunnel endpoint so the receiver carries TURN/TCP over its existing WSS connection:

1. Set `turn.tunnelTarget` in the Hub config to coturn's local TCP listener:

   ```json
   "turn": {
     "urls": ["turn:turn.example.com:3478?transport=udp"],
     "authSecretFile": "/etc/relaymic/turn-auth-secret",
     "tunnelTarget": "127.0.0.1:3478"
   }
   ```

2. Start the receiver with `-turn-tunnel`. It listens on loopback, rewrites the receiver's TURN URL to `turn:127.0.0.1:<port>?transport=tcp`, and sends each connection through `/ws/tunnel` to the Hub, which forwards it to coturn.

Boundaries:

- Only token-authenticated receivers can use the tunnel, and it can forward only to the configured fixed target.
- Audio remains end-to-end DTLS-SRTP encrypted. The Hub sees TURN protocol bytes, not audio content.
- The remote browser continues to use the original TURN URL sent by the Hub.
- coturn must listen on TCP locally (the default `listening-port=3478` listens on UDP and TCP), and `listening-ip` must include `127.0.0.1`.
- Without `tunnelTarget`, the endpoint returns 404. Without `-turn-tunnel`, receiver behavior is unchanged.
