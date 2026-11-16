# SteamOS capability research matrix
Claude Code must research and test on real supported SteamOS hardware before marking any uncertain action production-ready.

| Capability | Candidate source | Initial status |
|---|---|---|
| CPU/RAM/storage/uptime | /proc, /sys, filesystem | high-confidence research target |
| GPU/temperature/fan/battery | hardware-specific /sys and APIs | device-dependent |
| OS/session mode | os-release, login/session APIs | validate |
| Installed games | Steam libraryfolders and appmanifest ACF | read-only candidate |
| Game artwork | local Steam cache, optional metadata | validate paths/licensing |
| Current game | session state, processes, Decky bridge | validate |
| Launch game | Steam URI/client interface | validate confirmation |
| Close game | supported Steam/session interface | experimental |
| Downloads/update queue | Steam client state, optional Decky | experimental |
| Pause/resume/update games | supported Steam API, if any | experimental |
| Proton/tool versions | local registered compatibility tools | validate |
| Change compatibility | supported Steam UI/API | experimental |
| Move/uninstall game | supported Steam client operation | experimental |
| Docked state, display/audio | DRM, udev, session APIs | hardware-dependent |
| Suspend/reboot/shutdown | logind and authorized helpers | validate permissions |
| Wake from sleep/off | device/network capability | optional, never guaranteed |

Each capability records available/unsupported/disabled/unavailable/unknown, source, required privilege, supported SteamOS version, freshness, and tested hardware. No unsupported direct writes to Steam manifests/configuration or prefix deletion.
