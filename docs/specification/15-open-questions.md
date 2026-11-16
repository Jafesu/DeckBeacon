# Open questions and feasibility blockers
1. Can authenticated restricted device communication traverse Home Assistant Cloud remote URLs using supported HA APIs? Prove end-to-end; otherwise require alternate transport.
2. What exact Steam client interfaces reliably report download queue, updates and control actions across supported SteamOS versions?
3. Which Steam game launch/exit and Proton selection operations are officially supported and safe?
4. Can Gaming Mode display interactive notification actions through current supported Decky APIs? Define fallback.
5. What boot-time telemetry and network access can be provided safely without a logged-in Steam session? Test helper lifecycle and lingering alternatives.
6. Which SteamOS hardware models and architectures are officially in initial support matrix?
7. How are offline command expiry and update deferral represented to users?
8. What privacy defaults and retention windows should be set for session history and notifications?
9. What minimum supported Home Assistant, SteamOS and Decky versions will be advertised?
10. What HACS/frontend and Decky distribution mechanics are current at release time?
