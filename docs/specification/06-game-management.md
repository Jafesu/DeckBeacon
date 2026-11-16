# Steam game management
Eight required areas: installed library, download queue, remote launching, game updates, session tracking, artwork/metadata, Proton/compatibility tools, game storage.

Library: discover Steam libraries and appmanifest_*.acf read-only; App ID, name, installed path, build ID, size, timestamps, running/update state. Distinguish non-Steam shortcuts, tools and games. Handle removable/missing libraries and cache invalidation.

Downloads: distinguish downloading, paused, scheduled, patching, verifying, complete and unknown; show speed, progress, ETA and queue when sources exist. Do not claim completion before install ready. Controls experimental until verified.

Launch/close: require game installed, policy allowed, and supported Steam command; distinguish request accepted, process started, confirmed running. Close gracefully, force separately gated. Protect active gaming sessions.

Sessions: optional local play history, start/stop and suspend segmentation, monotonic durations, bounded retention and delete/export. Backfilled events must not trigger live automations unexpectedly.

Metadata: local artwork first, optional external fetch with user consent and cache. No Steam password collection.

Proton: enumerate registered compatibility tools and per-game overrides read-only first; changes only through validated supported interfaces. Never delete prefixes automatically.

Storage: per-library capacity, largest games, removable media; moving/uninstalling through Steam-managed operations only, with confirmation and safe error handling. Never directly delete game directories.
