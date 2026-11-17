# Remote access

DeckBeacon keeps reporting when your SteamOS device leaves home, as long as Home Assistant can be reached from the internet (ADR-0001, ADR-0011). The device always dials out to Home Assistant. Nothing listens on the device and no router port is opened for it.

## Requirements
You need at least one of these in Home Assistant:
- **Home Assistant Cloud** with remote UI enabled (Settings → Home Assistant Cloud). This is the path proven end to end in M0.
- **An external URL** (Settings → System → Network → Home Assistant URL → Internet) that is reachable from outside, e.g. your own domain with a reverse proxy.
- **An extra device address** (Settings → Devices & services → DeckBeacon → Configure → *Extra address for devices*), e.g. a VPN address like `http://100.64.0.10:8123` or a reverse proxy only your devices use. It must be `http://` or `https://`, without a username, password or query string.

Remote access doesn't need a Home Assistant token on the device and doesn't weaken pairing. Every connection, local or remote, uses the same Noise IK handshake with Home Assistant's key that was pinned at pairing. A server that doesn't hold that key can't complete it, so a wrong or hostile address simply fails to connect.

## How the device learns the addresses
- At pairing, Home Assistant sends its internal, external and Cloud addresses.
- **At every connection** it sends them again, plus the extra device address and the address the device is using right then. The device saves the new list.
  - If you turn on HA Cloud, change your external URL or move HA to a new IP, the device picks it up the next time it connects. No re-pairing needed.
  - The device only accepts this list from the authenticated session. It ignores an empty or invalid list and never drops the address it is connected through.
  - You need to re-pair only if the device can't reach Home Assistant by any address it knows.
- If Home Assistant's LAN address changes while the device is away, the device searches the local network (mDNS, `_home-assistant._tcp`) before falling back to remote. As above, an address found this way is used only if the pinned-key handshake succeeds.

## What happens when you leave and come back
| Situation | What the device does | Typical time |
|---|---|---|
| Wi-Fi off, or switched to another network | Notices the change within about 3 s, checks the connection (ping, 5 s deadline), tries local (5 s timeout) and then remote (20 s timeout) | well under 30 s |
| Network drops without the device noticing (e.g. a router reboot) | Detected by the heartbeat (15 s interval, 45 s timeout) | up to about 1 min |
| Back home | While remote, it probes local every 30 s and on every network change. It switches back after 3 successful probes in a row, so one lucky probe can't make it flap | about 90 s |

During a switch, the same device and the same Home Assistant entities are kept:
- The new session replaces the old one, which Home Assistant closes.
- Events (game started, download finished, …) carry sequence numbers. Home Assistant drops duplicates, and the device re-sends anything that was never acknowledged, so automations see each event once.

## What you can see
The **Connection** sensor shows `local`, `remote` or `offline`. Its `via` attribute shows which address is in use:

| `via` | Meaning |
|---|---|
| `internal` | HA's internal URL |
| `external` | HA's external URL |
| `cloud` | Home Assistant Cloud |
| `manual` | the address used at pairing, or the one the device connected through |
| `manual_remote` | the extra device address, or a remote address the device connected through |
| `discovered` | found on the LAN by mDNS |

## Permissions
Remote sessions get their own permission tier (Configure page):
- Game management (launch and close) is **off** remotely by default.
- Approved agent updates are **on** remotely by default (ADR-0010).
- Read-only monitoring is always allowed.

## Limits
- Remote access needs Home Assistant itself to be reachable. DeckBeacon doesn't relay through any other service.
- HA Cloud and reverse proxies add latency. Sensors update a little later than at home.
- If the device is asleep or offline, it reconnects when it wakes. Events that happened offline are sent then, marked as not live.
