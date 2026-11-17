Steam library fixture DERIVED from the Dell G15 discovery (docs/research/evidence/G15-m6-discovery-2026-11-17.md): app ids, names, StateFlags, sizes and timestamps are the recorded values for a subset of the 37 entries. appmanifest_1716740.acf (Starfield) is SYNTHETIC: changed to an in-progress update to test download parsing. config/config.vdf is a CANARY that must never be opened.

userdata/10000001/config/shortcuts.vdf is SYNTHETIC binary VDF in the shape recorded in discovery §11 (key set, types, signed int32 appids; the user id directory is made up). Names and appids follow the recorded entries; `Exe`, `StartDir`, `LaunchOptions` and `tags` hold CANARY values that tests assert never leave the parser. One entry is hidden and must be excluded.

logs/gameprocess_log.txt is a SYNTHETIC excerpt in the recorded line formats (§7–§12), including a launch line with a canary home path.
