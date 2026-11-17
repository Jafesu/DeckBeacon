# Agent updates

DeckBeacon's agent updates itself only with your approval (ADR-0010). The Home Assistant integration and the dashboard cards update separately through the HA Marketplace.

## How it works
1. **Check:** every 6 hours (and at start) the agent looks at the DeckBeacon releases on GitHub for its channel. It downloads only the small signed manifest, verifies the signature against the key built into the agent, and checks compatibility with your HA integration.
2. **Offer:** Home Assistant shows **Update available** on the device's "Agent" update entity, with a link to the release notes.
3. **Approve:** press **Install**. Nothing is ever installed without this, and the approval names that exact version. It is remembered for 7 days if the device can't apply it straight away.
4. **Wait for a safe moment:** the agent waits while a game is running or Steam is downloading. The update entity shows why it is waiting.
5. **Install:** the agent downloads the binary, checks its size and checksum against the signed manifest, and checks that it runs and reports the right version.
6. **Switch:** a separate updater process (a temporary `systemd --user` unit) switches to the new version and restarts the agent.
7. **Check health:** the updater confirms the new version starts and, if the device was connected before, reconnects to HA within 2 minutes.
8. **Roll back if needed:** otherwise it switches back to the previous version automatically. HA shows "Agent update failed (rolled back)" with the reason.

Your pairing, identity and settings are never touched by an update.

## Channels
Set in **Settings → Devices & services → DeckBeacon → Configure**:
- **stable** (default): tested releases.
- **beta**: early releases (`X.Y.Z-beta.N`).
- **dev**: development builds (`X.Y.Z-dev.N`). They may be unstable and are used for testing, including rollback tests.

## Permissions
Agent updates are their own permission tier. By default they are allowed both on the local network and remotely, because releases are signed and verified on the device and each install still needs your approval. You can turn remote updates off in the same options page.

## On the device
```bash
deckbeacon update status     # running version, managed versions, on/off
deckbeacon update check      # check now (prints what HA would show)
deckbeacon update off        # stop checking GitHub entirely (no network access for updates)
deckbeacon update on
deckbeacon update rollback   # switch back to the previous version and restart
```
Restart the agent after `on`/`off` (`systemctl --user restart deckbeacon.service`).

## Files
- `~/.local/share/deckbeacon/versions/<version>/deckbeacon`: installed versions (the active one and the previous one are kept).
- `~/.local/share/deckbeacon/bin/deckbeacon`: a link to the active version. The systemd unit starts this.
- `~/.local/share/deckbeacon/previous`: a link to the rollback target.
- An install built from source is copied into `versions/` on its first update and becomes the rollback target; the original file is left where it was.

## Network
Checking for updates is the agent's only internet access besides HA Cloud. It contacts `api.github.com` and GitHub's release download host. `deckbeacon update off` disables it.

## For maintainers: releasing
Bump `apps/agent/VERSION` on `main`. The **Agent release** workflow builds amd64 and arm64, writes and signs `release.json` with the `DECKBEACON_RELEASE_KEY` secret, verifies it against the key embedded in the agent, and publishes `agent/v<version>`. A `-beta.N` or `-dev.N` version is published as a pre-release. It refuses to publish unsigned.
