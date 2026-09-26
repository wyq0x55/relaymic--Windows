# RelayMic VPS Signaling and TURN Deployment Guide

> English · [简体中文](vps-signaling-turn-agent-guide.zh-CN.md)
>
> Goal: deploy RelayMic Signaling and coturn on an Ubuntu VPS with a public IPv4 address. This provides an HTTPS/WSS rendezvous point for a Windows Receiver and browser sender, plus TURN for restrictive NATs. This guide does not authorize changes to existing VPN, 3x-ui, Hysteria, SSH, or other services beyond the firewall rules explicitly approved by the user.

## Read first: code boundaries

`cmd/signaling` is production-capable: the browser and Receiver both connect out to one HTTPS/WSS endpoint. Audio does not pass through Signaling.

**Never put long-lived coturn usernames or passwords in `signaling.json` under `iceServers`.** `GET /api/ice` is public to anyone who can open the site; exposed static TURN credentials could let anyone consume your relay traffic. The current code issues the same short-lived TURN credentials to both peers only after pairing. Deploy in two phases:

1. Deploy Signaling, install and harden coturn, then verify coturn externally.
2. Deploy a RelayMic commit with short-lived TURN credential support, configure the restricted shared-secret file, and verify a real relay call with `-force-relay`.

Do not claim RelayMic can relay calls through symmetric NAT merely because the coturn service is running. That claim requires a successful two-peer `-force-relay` test.

## Agent contract

Before starting, request or confirm these values. **Never ask the user to paste tokens, private keys, or root passwords in plaintext into chat.**

| Variable | Example | Purpose |
| --- | --- | --- |
| `VPS_HOST` | `203.0.113.10` | SSH host or alias |
| `SIGNAL_FQDN` | `mic.example.com` | Signaling A/AAAA record |
| `TURN_FQDN` | `turn.example.com` | coturn A/AAAA record; may be the same host |
| `RELAYMIC_REPO` | `https://github.com/wyq0x55/relaymic--Windows.git` | Git repository containing the reviewed features |
| `RELAYMIC_REF` | Reviewed commit SHA | Exact RelayMic version to deploy |
| `RECEIVER_NAME` | `company-windows` | Stable display name for the Receiver |

Execution constraints:

1. Inventory read-only first. If ports `80`, `443`, `3478`, `5349`, or `49160-49200/udp` are occupied, stop and report the owner.
2. Do not stop, restart, or alter existing 3x-ui, Hysteria, sing-box, Tailscale, or SSH services. Let the user choose how to resolve port conflicts.
3. Store secrets only in files readable by root or the service account, with mode `0600`; do not print them to terminal logs, systemd status, Git, or chat.
4. Verify each phase before continuing. List exact ports and reasons before any external DNS, UFW, or cloud security-group change.

## Step 0: read-only inventory

Log in to the VPS and report a summary. Do not paste private keys, full configs, or passwords:

```bash
set -euo pipefail
uname -m
cat /etc/os-release
hostnamectl
ip -brief address
sudo ss -lntup
sudo systemctl --type=service --state=running --no-pager
sudo ufw status verbose || true
sudo nft list ruleset || true
```

Also confirm that `SIGNAL_FQDN` and `TURN_FQDN` resolve to this VPS's public address. Only create an AAAA record if the VPS has reachable IPv6. Verify that the planned ports below are unused:

| Port | Protocol | Service | Purpose |
| --- | --- | --- | --- |
| `80` | TCP | Caddy | ACME HTTP-01 certificate issuance and HTTPS redirect |
| `443` | TCP | Caddy | Browser HTTPS and WSS Signaling |
| `3478` | UDP + TCP | coturn | STUN/TURN allocation |
| `49160-49200` | UDP | coturn | Relayed media data ports |

Add `5349` (TURN/TLS) only if coturn has its own certificate, the port is free, and a TCP-only network requires it. Do not use Caddy to proxy UDP/TURN traffic.

## Step 1: install runtime and pin the version

The example targets Ubuntu 24.04 on `amd64`. Stop and adapt package installation and the official Go archive for non-`amd64` or non-Debian systems. Do not use an outdated distribution Go package instead of the version required by the repository.

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl git caddy coturn ufw

go version || true
```

If Go is older than the version in `go.mod`, download the matching **official** Go archive from `https://go.dev/dl/`. Verify its SHA-256 against the official download listing, then install it under `/usr/local/go`. In a new shell, confirm:

```bash
export PATH=/usr/local/go/bin:$PATH
go version
```

Create a least-privilege service account and directories:

```bash
sudo useradd --system --home /opt/relaymic --shell /usr/sbin/nologin relaymic \
  2>/dev/null || true
sudo install -d -o relaymic -g relaymic -m 0750 /opt/relaymic /etc/relaymic
```

Check out the user's immutable commit. Do not deploy uncommitted local changes or a floating branch head:

```bash
sudo -u relaymic git clone "$RELAYMIC_REPO" /opt/relaymic/src
sudo -u relaymic git -C /opt/relaymic/src checkout --detach "$RELAYMIC_REF"
sudo -u relaymic git -C /opt/relaymic/src status --porcelain
sudo -u relaymic env PATH=/usr/local/go/bin:$PATH \
  go -C /opt/relaymic/src build -trimpath -buildvcs=true \
  -o /opt/relaymic/relaymic-signaling ./cmd/signaling
sudo chown relaymic:relaymic /opt/relaymic/relaymic-signaling
sudo chmod 0755 /opt/relaymic/relaymic-signaling
```

`git status --porcelain` must be empty. If the repository or commit differs from the value supplied by the user, stop and report it; do not guess a branch.

## Step 2: deploy HTTPS/WSS Signaling

Use Caddy to terminate TLS and keep RelayMic on loopback HTTP. This avoids giving RelayMic access to the TLS private key or requiring it to bind port `443` as root. Caddy supports WebSocket proxying natively.

Create `/etc/relaymic/signaling.json`. `iceServers` must contain only public STUN servers; keep the TURN shared secret in a restricted file:

```json
{
  "listen": "127.0.0.1:8080",
  "allowedOrigins": ["mic.example.com"],
  "iceServers": [
    {"urls": ["stun:stun.cloudflare.com:3478"]}
  ],
  "turn": {
    "urls": ["turn:turn.example.com:3478?transport=udp"],
    "authSecretFile": "/etc/relaymic/turn-auth-secret",
    "credentialTTLSeconds": 600
  },
  "receivers": [
    {"name": "company-windows", "token": "REPLACE_WITH_GENERATED_TOKEN"}
  ],
  "adminTokenFile": "/etc/relaymic/admin-token",
  "receiversFile": "/var/lib/relaymic/receivers.json"
}
```

### Optional admin page

`/admin` exists only when both `adminTokenFile` and `receiversFile` are set; if neither is configured it returns 404. With the admin page enabled, adding a receiver does not require a config edit or restart (a restart disconnects active calls).

```bash
set -euo pipefail
sudo install -o root -g root -m 0600 /dev/null /root/relaymic-admin-token
sudo /opt/relaymic/relaymic-signaling -gen-admin-token |
  sudo tee /root/relaymic-admin-token >/dev/null
sudo install -o relaymic -g relaymic -m 0600 \
  /root/relaymic-admin-token /etc/relaymic/admin-token
sudo rm /root/relaymic-admin-token   # after handing it to the administrator
```

The directory containing `receiversFile` must be writable by the service account. The unit below uses `ProtectSystem=strict`, which makes `/var/lib` read-only **unless** it is explicitly writable or managed by systemd. Choose one:

- Add `StateDirectory=relaymic` to the unit (recommended; systemd creates `/var/lib/relaymic` and allows writes there).
- Or set `receiversFile` to an already writable directory, such as `/opt/relaymic/receivers.json`.

To fix an existing unit, either add a drop-in (recommended) or edit the original service file:

```bash
# In the editor, add under [Service]:
#   StateDirectory=relaymic
sudo systemctl edit relaymic-signaling

# Or add a line directly to the existing unit:
sudo sed -i '/^\[Service\]/a StateDirectory=relaymic' \
  /etc/systemd/system/relaymic-signaling.service

sudo systemctl daemon-reload && sudo systemctl restart relaymic-signaling
systemctl show relaymic-signaling -p StateDirectory   # should print relaymic
ls -ld /var/lib/relaymic                              # should be owned by relaymic
```

The Hub checks the directory at startup. If it cannot write there, it logs a warning; generating a token in the admin page returns HTTP 500 with a reason.

`allowedOrigins` contains hostnames only, without `https://`. Generate a receiver token and write its output to a restricted file:

```bash
set -o pipefail
sudo install -o root -g root -m 0600 /dev/null \
  /root/relaymic-receiver-bootstrap.json
sudo -u relaymic /opt/relaymic/relaymic-signaling \
  -gen-receiver "$RECEIVER_NAME" |
  sudo tee /root/relaymic-receiver-bootstrap.json >/dev/null
test -s /root/relaymic-receiver-bootstrap.json
```

The Agent must extract the token from this JSON **on the server only** and put it in the matching Receiver entry in `signaling.json`. Then set file ownership and permissions:

```bash
sudo chown relaymic:relaymic /etc/relaymic/signaling.json
sudo chmod 0600 /etc/relaymic/signaling.json
```

Keep `/root/relaymic-receiver-bootstrap.json` until the Windows Receiver has safely stored the token. Delete it only after the user confirms. Never pass the token as a `-token` command-line argument.

Create `/etc/systemd/system/relaymic-signaling.service`:

```ini
[Unit]
Description=RelayMic signaling service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=relaymic
Group=relaymic
ExecStart=/opt/relaymic/relaymic-signaling -config /etc/relaymic/signaling.json -plain
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/opt/relaymic
# Persist the receiver list used by the admin page.
StateDirectory=relaymic

[Install]
WantedBy=multi-user.target
```

Add a Caddy site block, replacing the hostname with the actual `SIGNAL_FQDN`:

```caddyfile
mic.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

The Caddy config path depends on the existing installation. Validate before reloading; do not overwrite existing sites:

```bash
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl daemon-reload
sudo systemctl enable --now relaymic-signaling
sudo systemctl reload caddy
sudo systemctl status relaymic-signaling caddy --no-pager
curl --fail --silent --show-error https://$SIGNAL_FQDN/api/ice
```

If port `443` is already used by Nginx, Caddy, or another service, do not replace it. Report the current TLS terminator and site config, then let the user choose whether to reuse the proxy or use another hostname/port.

## Step 3: install and harden coturn

Generate a random TURN REST API shared secret that stays on the VPS:

```bash
sudo install -d -o root -g relaymic -m 0750 /etc/relaymic
sudo sh -c 'umask 077; openssl rand -base64 48 > /etc/relaymic/turn-auth-secret'
sudo chown root:relaymic /etc/relaymic/turn-auth-secret
sudo chmod 0640 /etc/relaymic/turn-auth-secret
```

Create `/etc/turnserver.conf`. Replace `TURN_PUBLIC_IP` and `TURN_FQDN` with real values. If the VPS owns the public IP directly, set `external-ip` to that address. For 1:1 NAT, use `external-ip=PRIVATE_IP/PUBLIC_IP` and first confirm the provider's mapping:

```ini
listening-port=3478
listening-ip=TURN_PUBLIC_IP
relay-ip=TURN_PUBLIC_IP
external-ip=TURN_PUBLIC_IP
realm=turn.example.com
server-name=turn.example.com

fingerprint
lt-cred-mech
use-auth-secret
static-auth-secret=REPLACE_WITH_CONTENTS_OF_SECRET_FILE
stale-nonce
no-cli
no-tlsv1
no-tlsv1_1
no-loopback-peers
no-multicast-peers
min-port=49160
max-port=49200
user-quota=12
total-quota=48
```

`static-auth-secret` must exactly match `/etc/relaymic/turn-auth-secret`. Have root write the coturn config from a protected file; never put the secret in a command line, Git, screenshot, chat, or RelayMic JSON. Keep `turnserver.conf` at mode `0600`; only root and the `relaymic` group may read the shared-secret file.

```bash
sudo chmod 0600 /etc/turnserver.conf
sudo systemctl enable --now coturn
sudo systemctl status coturn --no-pager
sudo ss -lntup | grep -E ':(3478|49160|49161)([[:space:]]|$)' || true
```

Open only these ports in the cloud security group and host firewall:

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 3478/tcp
sudo ufw allow 3478/udp
sudo ufw allow 49160:49200/udp
sudo ufw status numbered
```

If UFW is disabled, record that and inspect nftables and the cloud security group. Do not clear, replace, or enable an unknown firewall policy for consistency.

### External TURN verification

Run this test from outside the VPS. A successful test on the VPS itself does not prove that cloud firewall rules or the relay port range are reachable externally.

1. On the VPS, create one-time coturn REST API `username` and `password` values with a ten-minute lifetime. Use username format `<unix-expiry>:relaymic-smoke`; the password is the Base64-encoded HMAC-SHA1 of the username with the shared secret. Give only the short-lived values to the test machine; never disclose the shared secret.
2. In a Windows RelayMic checkout, build and run:

   ```powershell
   go run ./cmd/turncheck -server "$TURN_FQDN`:3478" `
     -user '<short-lived username>' -pass '<short-lived password>' -realm "$TURN_FQDN"
   ```

3. coturn's data path passes only when output includes `STUN 绑定 ✓`, `TURN 分配 ✓`, and `中继转发 ✓` (the current `turncheck` prints these labels in Chinese). If allocation succeeds but forwarding fails, `49160-49200/udp` is commonly blocked.
4. Let the test credentials expire. Do not reuse them for production.

## Step 4: connect RelayMic and verify

1. Pin `RELAYMIC_REF` to a commit containing the short-lived TURN credential support, then build and replace the Hub binary.
2. Keep `iceServers` limited to STUN and configure the `turn` object above. The `relaymic` service account must be able to read `authSecretFile`.
3. After restarting the Hub, verify `/api/ice` still returns only STUN and contains no TURN username or password.
4. After pairing, the Hub sends the same ten-minute coturn REST API credentials to the browser and Receiver. The Receiver no longer needs `-turn-user` or `-turn-pass`.
5. On Windows, run a real two-way call between browser microphone and Teams return audio with `-force-relay`. Do not claim symmetric-NAT support until this test succeeds.

## Step 5: handoff and Windows startup

The deployment Agent should report only this non-sensitive evidence:

1. DNS resolution, `https://SIGNAL_FQDN/api/ice`, and the result of `systemctl is-active relaymic-signaling caddy coturn`.
2. Actual listening ports, the exact firewall/security-group rules added, and confirmation that existing services were not touched.
3. The three successful external `turncheck` markers, with no username or password.
4. RelayMic commit SHA, binary SHA-256, systemd unit, and config-file permissions.
5. Any outstanding work, especially real `-force-relay` end-to-end verification if it was not completed.

Transfer the Receiver token through a controlled channel and save it in a file readable only by the current Windows user, for example `C:\RelayMic\receiver-token.txt`. Then start the receiver (adjust the executable path to match the delivered file):

```powershell
C:\RelayMic\relaymic.exe receiver `
  -hub "wss://SIGNAL_FQDN/ws/receiver" `
  -token-file "C:\RelayMic\receiver-token.txt" `
  -device "CABLE Input" `
  -return-device "VoiceMeeter Aux Output" `
  -monitor "127.0.0.1:7420" `
  -force-relay
```

On the Receiver PC, open `http://127.0.0.1:7420/monitor` to read the one-time pairing code. It is shown only on loopback and is not written to runtime logs. On a sender device, open `https://SIGNAL_FQDN`, enter the code, and grant microphone access. Verify direct connectivity first, then verify two-way audio over coturn with `-force-relay`. Only after that can support for restrictive public networks be considered complete.

## HTTP-proxy-only corporate networks

If the Receiver can reach the Internet only through an HTTP proxy (DNS/STUN/TURN UDP and direct TCP are blocked), the standard connection will fail because the TURN client does not use the proxy. Use the TURN tunnel:

1. In the Hub's `signaling.json`, add a `tunnelTarget` under `turn` that points to coturn's local TCP listener:

   ```json
   "tunnelTarget": "127.0.0.1:3478"
   ```

   Confirm coturn's `listening-ip` includes `127.0.0.1`; the tunnel cannot connect if coturn listens only on the public IP.

2. Start the Receiver with `-turn-tunnel` and set the HTTP proxy with `-proxy` (or `HTTP_PROXY` / `HTTPS_PROXY`):

   ```powershell
   C:\RelayMic\relaymic.exe receiver -hub "wss://SIGNAL_FQDN/ws/receiver" `
     -token-file "C:\RelayMic\receiver-token.txt" `
     -device "CABLE Input" -return-device "VoiceMeeter Aux Output" `
     -monitor "127.0.0.1:7420" -turn-tunnel -force-relay
   ```

3. Verify the Receiver log shows `TURN tunnel: 127.0.0.1:<port> → wss://...`, the selected path is relay after pairing, and audio works in both directions. The tunnel reuses the WSS port in the Hub URL (port `443` with this Caddy setup); no extra inbound port is required.

This solves the Receiver-side restriction only. If the sender's browser is on the same kind of network, its own outbound connectivity must also be addressed.
