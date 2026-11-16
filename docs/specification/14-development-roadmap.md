# Claude Code implementation roadmap
0. Research/feasibility: SteamOS hardware interfaces, Steam APIs/Decky, HA Cloud authenticated transport, startup behavior and security. Stop and report unsupported assumptions.
1. Monorepo foundation: AGENTS.md, CLAUDE.md, CI, protocol schemas, test fixtures, architecture decisions.
2. Local MVP: Go user agent, discovery, pairing, secure HA integration, core system telemetry and multi-device registry.
3. Boot-time option: minimal boot helper, handoff, permissions, boot/login/suspend tests.
4. Decky: pairing UI, agent status, notifications, supported Steam adapter.
5. Remote: validated outbound transport, URL discovery, fallback, security and offline replay.
6. Game management: library and sessions first; downloads/launch next; Proton/storage destructive operations only after validation.
7. Notifications and HA automation: events, notify, interactive responses, device triggers.
8. Dashboard: optional cards, media proxy, responsive multi-device interface.
9. Managed updates: signed releases, independent updater, rollback, channel support.
10. Hardening/public release: HACS/Decky packaging, docs, accessibility, security and regression testing.

At each phase: write plan, implement only approved phase, test, document evidence, update capability matrix, request decision if blocked. Do not implement speculative unsafe interfaces as if production-ready.
