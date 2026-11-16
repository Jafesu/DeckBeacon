# Milestone 0 — Owner Decisions (2026-11-16)

These answer §8 of the [feasibility report](M0-feasibility-report.md). They are binding for v1 unless superseded by a later ADR.

| # | Question | Decision | Consequence |
|---|---|---|---|
| D1 | ADR-0001…0006 | **Approved as written.** | All six ADRs are `Accepted`. |
| D2 | Pairing in Gaming Mode without Decky | **Ideal, but not required for v1.** v1 confirms the pairing code through the CLI, the Desktop Mode loopback UI, or the Decky plugin. | Gaming-Mode-without-Decky pairing is tracked as a post-v1 enhancement (candidate: non-Steam "DeckBeacon Pairing" shortcut with a gamepad-navigable window). |
| D3 | close / pause-resume / compat tool / move | **Ship as experimental**: Decky-only, disabled by default, per-capability opt-in, Steam-build gated (ADR-0004 Tier C). | HA shows these as `disabled (experimental)` or `unsupported (requires Decky)`. |
| D4 | Minimum HA version | **HA ≥ 2026.5.** The Marketplace distribution path requires ≥ 2026.11. Earlier versions install manually. | `manifest.json`/`hacs.json` minimum = 2026.5. CI tests the minimum and the latest. |
| D5 | Hardware support | **Tier 1: Steam Deck LCD, Steam Deck OLED, and genuine SteamOS on non-Valve hardware.** The project's test machine is a **Dell G15 laptop running SteamOS**. | Collectors must detect capabilities, not hard-code Valve hardware. No `board_name` allowlists. The GPU, thermal, fan, and battery providers each need a generic path (amdgpu, NVIDIA, Intel, `coretemp`/`k10temp`, `dell_smm_hwmon`, any `power_supply` battery). "Genuine SteamOS" means `/etc/os-release` `ID=steamos`. Derivatives (Bazzite, ChimeraOS, HoloISO, etc.) are out of scope and reported as unsupported. |
| D6 | HA Cloud end-to-end test (HV-R1) | **Approved**: one-time, temporary install of the spike integration on the owner's HA, then removal. | See HV-R1 results in `evidence/`. |
| D7 | SteamOS device access | **Not available yet**. Access to the Dell G15 will be provided later. | M1/M2 proceed with mocked providers and recorded fixtures. Hardware rows stay `research` until the hardware validation plan runs on the G15 (and a Deck when available). |

## Implications for the Dell G15 (non-Valve Tier 1)

The G15 family ships with Intel or AMD CPUs and NVIDIA GeForce GPUs (some with an integrated GPU too). This needs validation on the actual unit:

- **GPU telemetry:** `gpu_busy_percent` is amdgpu-only. NVIDIA needs NVML/`nvidia-smi` (proprietary driver) or nouveau hwmon (limited). Utilisation may be `unavailable` and must be reported that way, never guessed.
- **Thermals and fans:** `coretemp` (Intel) or `k10temp` (AMD). Fans via `dell_smm_hwmon` if the kernel module loads.
- **Battery:** `/sys/class/power_supply/BAT0` (Deck uses `BAT1`), so enumerate rather than hard-code.
- **Display/dock semantics:** a laptop has no dock in the Deck sense. External display detection still applies, so `docked` maps to "external display connected" with `confidence=heuristic`.
- **Steam paths, session mode, gamescope:** expected to match the Deck, but must be confirmed.
- **Wake:** Ethernet WoL may be available on the G15 (unlike Deck Wi-Fi). Per-device opt-in test.
