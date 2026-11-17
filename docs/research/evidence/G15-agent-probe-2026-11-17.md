# G15 agent probe: real agent on the Tier-1 non-Valve reference unit

- **Device:** Dell G15 5520 running SteamOS 3.9.5 (build 20261023.100, `rel` branch). Hardware details are in [G15-capture-2026-11-17.md](G15-capture-2026-11-17.md).
- **Captured:** 2026-11-17 UTC (2026-11-16 local evening). The machine had booted at 2026-11-17T00:40:42Z.
- **Repo:** branch `claude/jolly-mendel-9ig3mz` at `673ce5d`
- **Mode:** **Desktop Mode** for every run (`kwin_wayland` and `plasmashell` running, no `gamescope`). The load run used a game launched in Desktop Mode.
- **Approval:** the user confirmed in the local session before Go was installed. Nothing used sudo, installed system-wide, paired, ran `deckbeacon service`, ran `loginctl` or triggered a power action. Nothing was committed or pushed. No shell rc files were edited.
- **Redactions:** username → `<user>`. The agent `device_id` → `<device-id>` (a 26-character lowercase base32 string). No hostname, IP, MAC or serial numbers appear.

## 1. Checkout update

```
$ git fetch origin claude/jolly-mendel-9ig3mz
   084e013..673ce5d  claude/jolly-mendel-9ig3mz -> origin/claude/jolly-mendel-9ig3mz
$ git diff --no-index --stat <(git show FETCH_HEAD:docs/research/evidence/G15-capture-2026-11-17.md) docs/research/evidence/G15-capture-2026-11-17.md && echo IDENTICAL
IDENTICAL
$ rm docs/research/evidence/G15-capture-2026-11-17.md
$ git pull --ff-only
$ git log --oneline -1
673ce5d G15 read-only hardware capture; fix NVIDIA decimal parsing; labelled GPU fallback; deckbeacon probe
```

## 2. Go toolchain in user space

```
$ ls ~/.local/go
ls: cannot access '/home/<user>/.local/go': No such file or directory      (no prior install)
$ curl -fsSL -o go.tgz https://go.dev/dl/go1.26.11.linux-amd64.tar.gz
$ curl -fsSL 'https://go.dev/dl/?mode=json&include=all' -o dl.json
$ python3 -I -c '<select version==go1.26.11, filename==go1.26.11.linux-amd64.tar.gz, print sha256>'
published 9849f4e04ee35303714e34fbc3ac8ab44ea3e448a95aa182f91e3b9b79c710ec
$ sha256sum go.tgz
9849f4e04ee35303714e34fbc3ac8ab44ea3e448a95aa182f91e3b9b79c710ec  go.tgz
```

**The SHA-256 matches the value go.dev publishes:** `9849f4e04ee35303714e34fbc3ac8ab44ea3e448a95aa182f91e3b9b79c710ec`. The tarball was downloaded into the session scratch directory, not the repo.

```
$ tar -C ~/.local -xzf go.tgz          # → ~/.local/go
$ export PATH=$HOME/.local/go/bin:$PATH   # this task only
$ go version
go version go1.26.11 linux/amd64
```

## 3. Build and version

```
$ cd apps/agent && mkdir -p ~/.local/share/deckbeacon/bin
$ CGO_ENABLED=0 go build -o ~/.local/share/deckbeacon/bin/deckbeacon ./cmd/deckbeacon
go: downloading github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
go: downloading golang.org/x/net v0.62.0
go: downloading github.com/godbus/dbus/v5 v5.2.4
go: downloading github.com/coder/websocket v1.8.14
go: downloading go.etcd.io/bbolt v1.4.3
go: downloading github.com/flynn/noise v1.1.0
go: downloading golang.org/x/text v0.44.0
go: downloading golang.org/x/crypto v0.59.0
go: downloading golang.org/x/sys v0.50.0
(build exit 0)
$ ~/.local/share/deckbeacon/bin/deckbeacon version
deckbeacon-agent 0.0.0-dev (commit unknown, protocol v1, linux/amd64)
```

Note: the module downloads went to the default `~/go/pkg/mod` cache (GOPATH was not overridden).

## 4. Agent read-only runs (idle, Desktop Mode)

```
$ ps -eo comm | grep -Ei 'gamescope|plasmashell|kwin' | sort | uniq -c
      1 kwin_wayland
      1 kwin_wayland_wr
      1 plasmashell
$ ls ~/.local/state/deckbeacon
ls: cannot access '/home/<user>/.local/state/deckbeacon': No such file or directory     (before first probe)
```

### 4a. `deckbeacon probe`

```
$ deckbeacon probe
device: map[arch:x86_64 model:Dell G15 5520 os:map[build_id:20261023.100 id:steamos variant_id:steamdeck version_id:3.9.5] vendor:Dell Inc.]

metrics:
  cpu.usage                  21.9           %    [/proc/stat]
  memory.used_percent        30.8           %    [/proc/meminfo]
  memory.total_bytes         33294299136    B    [/proc/meminfo]
  system.boot_time           2026-11-17T00:40:42Z      [/proc/stat btime]
  storage.home.used_percent  56.2           %    [statfs /home]
  storage.home.free_bytes    868599443456   B    [statfs /home]
  battery.level              100            %    [power_supply BAT0]
  battery.charging           false               [power_supply BAT0]
  battery.status             full                [power_supply BAT0]
  battery.power_w            0              W    [power_supply BAT0]
  power.ac_online            true                [power_supply]
  thermal.cpu_c              56             °C   [hwmon coretemp]
  fan.rpm                    2313           rpm  [hwmon alienware_wmi]
  gpu.busy                   10             %    [nvidia-smi]
  thermal.gpu_c              54.2           °C   [nvidia-smi]
  session.mode               desktop             [process scan]

capabilities:
  telemetry.cpu              available    
  telemetry.memory           available    
  telemetry.storage          available    
  telemetry.battery          available    
  telemetry.thermal.cpu      available    
  telemetry.thermal.gpu      available    
  telemetry.gpu.busy         available    
  telemetry.fan              available    
  session.mode               available    
  game.library               unsupported  not implemented in this agent version (planned M6)
  game.launch                unsupported  not implemented in this agent version (planned M6)
  game.current               unsupported  not implemented in this agent version (planned M6)
  downloads.status           unsupported  not implemented in this agent version (planned M6)
  notifications.display      unsupported  not implemented in this agent version (planned M7)
  power.suspend              unsupported  not implemented in this agent version (planned M7)
  power.reboot               unsupported  not implemented in this agent version (planned M7)
  power.shutdown             unsupported  not implemented in this agent version (planned M7)
  power.wake                 unsupported  wake from sleep/off is not supported (spec 02: never guaranteed)
  lifecycle.sleep_notify     available    
  lifecycle.shutdown_notify  available    
  lifecycle.resume_detect    available    
  service.start_at_boot      disabled     opt-in: deckbeacon service boot on
exit=0
```

### 4a. `deckbeacon probe --json`

```json
{
  "boot_id_ok": true,
  "capabilities": [
    { "id": "telemetry.cpu", "status": "available", "tier": "system", "source": "/proc/stat", "privilege": "none" },
    { "id": "telemetry.memory", "status": "available", "tier": "system", "source": "/proc/meminfo", "privilege": "none" },
    { "id": "telemetry.storage", "status": "available", "tier": "system", "source": "statfs /home", "privilege": "none" },
    { "id": "telemetry.battery", "status": "available", "tier": "system", "source": "power_supply BAT0", "privilege": "none" },
    { "id": "telemetry.thermal.cpu", "status": "available", "tier": "system", "source": "hwmon coretemp", "privilege": "none" },
    { "id": "telemetry.thermal.gpu", "status": "available", "tier": "system", "source": "nvidia-smi", "privilege": "none" },
    { "id": "telemetry.gpu.busy", "status": "available", "tier": "system", "source": "nvidia-smi", "privilege": "none" },
    { "id": "telemetry.fan", "status": "available", "tier": "system", "source": "hwmon alienware_wmi", "privilege": "none" },
    { "id": "session.mode", "status": "available", "tier": "system", "source": "process scan", "privilege": "none" },
    { "id": "game.library", "status": "unsupported", "tier": "none", "source": "none", "reason": "not implemented in this agent version (planned M6)" },
    { "id": "game.launch", "status": "unsupported", "tier": "none", "source": "none", "reason": "not implemented in this agent version (planned M6)" },
    { "id": "game.current", "status": "unsupported", "tier": "none", "source": "none", "reason": "not implemented in this agent version (planned M6)" },
    { "id": "downloads.status", "status": "unsupported", "tier": "none", "source": "none", "reason": "not implemented in this agent version (planned M6)" },
    { "id": "notifications.display", "status": "unsupported", "tier": "none", "source": "none", "reason": "not implemented in this agent version (planned M7)" },
    { "id": "power.suspend", "status": "unsupported", "tier": "none", "source": "none", "reason": "not implemented in this agent version (planned M7)" },
    { "id": "power.reboot", "status": "unsupported", "tier": "none", "source": "none", "reason": "not implemented in this agent version (planned M7)" },
    { "id": "power.shutdown", "status": "unsupported", "tier": "none", "source": "none", "reason": "not implemented in this agent version (planned M7)" },
    { "id": "power.wake", "status": "unsupported", "tier": "none", "source": "none", "reason": "wake from sleep/off is not supported (spec 02: never guaranteed)" },
    { "id": "lifecycle.sleep_notify", "status": "available", "tier": "system", "source": "logind delay inhibitor", "privilege": "none" },
    { "id": "lifecycle.shutdown_notify", "status": "available", "tier": "system", "source": "logind delay inhibitor", "privilege": "none" },
    { "id": "lifecycle.resume_detect", "status": "available", "tier": "system", "source": "logind PrepareForSleep", "privilege": "none" },
    { "id": "service.start_at_boot", "status": "disabled", "tier": "system", "source": "systemd user lingering", "privilege": "user", "reason": "opt-in: deckbeacon service boot on" }
  ],
  "device": {
    "arch": "x86_64",
    "model": "Dell G15 5520",
    "os": { "build_id": "20261023.100", "id": "steamos", "variant_id": "steamdeck", "version_id": "3.9.5" },
    "vendor": "Dell Inc."
  },
  "metrics": [
    { "key": "cpu.usage", "value": 8.2, "unit": "%", "sampled_at": "2026-11-17T00:59:02Z", "source": "/proc/stat", "confidence": "confirmed" },
    { "key": "memory.used_percent", "value": 30.8, "unit": "%", "sampled_at": "2026-11-17T00:59:02Z", "source": "/proc/meminfo", "confidence": "confirmed" },
    { "key": "memory.total_bytes", "value": 33294299136, "unit": "B", "sampled_at": "2026-11-17T00:59:02Z", "source": "/proc/meminfo", "confidence": "confirmed" },
    { "key": "system.boot_time", "value": "2026-11-17T00:40:42Z", "sampled_at": "2026-11-17T00:59:02Z", "source": "/proc/stat btime", "confidence": "confirmed" },
    { "key": "storage.home.used_percent", "value": 56.2, "unit": "%", "sampled_at": "2026-11-17T00:59:02Z", "source": "statfs /home", "confidence": "confirmed" },
    { "key": "storage.home.free_bytes", "value": 868598398976, "unit": "B", "sampled_at": "2026-11-17T00:59:02Z", "source": "statfs /home", "confidence": "confirmed" },
    { "key": "battery.level", "value": 100, "unit": "%", "sampled_at": "2026-11-17T00:59:02Z", "source": "power_supply BAT0", "confidence": "confirmed" },
    { "key": "battery.charging", "value": false, "sampled_at": "2026-11-17T00:59:02Z", "source": "power_supply BAT0", "confidence": "confirmed" },
    { "key": "battery.status", "value": "full", "sampled_at": "2026-11-17T00:59:02Z", "source": "power_supply BAT0", "confidence": "confirmed" },
    { "key": "battery.power_w", "value": 0, "unit": "W", "sampled_at": "2026-11-17T00:59:02Z", "source": "power_supply BAT0", "confidence": "high" },
    { "key": "power.ac_online", "value": true, "sampled_at": "2026-11-17T00:59:02Z", "source": "power_supply", "confidence": "high" },
    { "key": "thermal.cpu_c", "value": 55, "unit": "°C", "sampled_at": "2026-11-17T00:59:02Z", "source": "hwmon coretemp", "confidence": "confirmed" },
    { "key": "fan.rpm", "value": 2313, "unit": "rpm", "sampled_at": "2026-11-17T00:59:02Z", "source": "hwmon alienware_wmi", "confidence": "confirmed" },
    { "key": "gpu.busy", "value": 5, "unit": "%", "sampled_at": "2026-11-17T00:59:02Z", "source": "nvidia-smi", "confidence": "confirmed" },
    { "key": "thermal.gpu_c", "value": 54.4, "unit": "°C", "sampled_at": "2026-11-17T00:59:02Z", "source": "nvidia-smi", "confidence": "confirmed" },
    { "key": "session.mode", "value": "desktop", "sampled_at": "2026-11-17T00:59:02Z", "source": "process scan", "confidence": "heuristic" }
  ]
}
exit=0
```

The JSON keeps the original keys and values, but each object is collapsed onto one line to save space.

### 4b. `deckbeacon status --json`

```
$ deckbeacon status --json
{"agent":"deckbeacon-agent 0.0.0-dev (commit unknown, protocol v1, linux/amd64)","device_id":"<device-id>","paired_with":null}
exit=0

$ ls -la ~/.local/state/deckbeacon
drwx------ 3 <user> <user> 4096 Nov 16 18:59 .
drwx------ 5 <user> <user> 4096 Nov 16 18:59 ..
drwx------ 2 <user> <user> 4096 Nov 16 18:59 identity
```

The first probe created the identity directory with mode 0700. The agent is not paired.

## 5. Agent tests on this hardware

```
$ go test ./internal/collect ./internal/lifecycle ./internal/service -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
--- PASS: TestSteamDeckOLED (0.00s)
--- PASS: TestCPUUsageAcrossSamples (0.00s)
--- PASS: TestDellG15WithNvidia (0.00s)
--- PASS: TestDellG15WithoutNvidiaDriverReportsUnavailable (0.00s)
--- PASS: TestNotSteamOSRefused (0.00s)
--- PASS: TestEmptyRootDegradesGracefully (0.00s)
--- PASS: TestDellG15RecordedCapture (0.00s)
--- PASS: TestG15WithoutNvidiaToolFallsBackToLabelledGPUSensor (0.00s)
ok  	github.com/jafesu/deckbeacon/apps/agent/internal/collect	0.005s
FAIL	github.com/jafesu/deckbeacon/apps/agent/internal/lifecycle [build failed]
--- PASS: TestInstallIdempotentAndUninstall (0.00s)
--- PASS: TestDoesNotDisableForeignLinger (0.00s)
--- PASS: TestLingerDeniedIsReported (0.00s)
--- PASS: TestRenderEscaping (0.00s)
--- PASS: TestTemplateMatchesPackagingFile (0.00s)
ok  	github.com/jafesu/deckbeacon/apps/agent/internal/service	0.002s
FAIL
exit=1
```

The cause of the lifecycle build failure:

```
$ go vet ./internal/lifecycle
# runtime/cgo
gcc: fatal error: cannot execute 'cc1': posix_spawnp: No such file or directory
compilation terminated.
```

On stock SteamOS, `gcc` is installed but its `cc1` back end is missing. Under the default `CGO_ENABLED=1`, something in the lifecycle test's build pulls in `runtime/cgo`, so the package fails to build. The test probably imports `os/user` or `net` while starting the private dbus-daemon. The run with cgo disabled:

```
$ CGO_ENABLED=0 go test ./internal/lifecycle -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
--- PASS: TestGapIsZeroWithoutSuspend (0.00s)
--- PASS: TestClockWatchThreshold (0.00s)
--- PASS: TestLogindSleepResumeShutdown (0.62s)
--- PASS: TestLogindUnavailable (0.01s)
--- PASS: TestClockWatchRunStops (0.05s)
ok  	github.com/jafesu/deckbeacon/apps/agent/internal/lifecycle	0.679s
```

**All 18 tests pass with `CGO_ENABLED=0`.** Suggested follow-up: document `CGO_ENABLED=0` for local test runs on SteamOS, or set it in a Makefile or script. The agent binary itself is already built with cgo disabled.

## 6. Load run (Desktop Mode, game running)

The user launched **Castle Crashers** (appid 204360, Proton) from Steam in Desktop Mode. It is a light 2D game.

```
$ ps -eo comm | grep -Ei 'gamescope|plasmashell|kwin' | sort | uniq -c
      1 kwin_wayland
      1 kwin_wayland_wr
      1 plasmashell
$ ps -eo args | grep -E 'reaper SteamLaunch' | grep -v grep | grep -oE 'AppId=[0-9]+'
AppId=204360
AppId=204360
AppId=204360
$ grep -E '^\s*"name"' ~/.local/share/Steam/steamapps/appmanifest_204360.acf
	"name"		"Castle Crashers"
```

The game rendered on the NVIDIA dGPU. This is an excerpt of the `nvidia-smi` process table, with other processes omitted:

```
|    0   N/A  N/A           13312    C+G   ...mon\CastleCrashers\castle.exe       1189MiB |
```

```
$ deckbeacon probe            # metrics section
  cpu.usage                  7.4            %    [/proc/stat]
  memory.used_percent        30.9           %    [/proc/meminfo]
  memory.total_bytes         33294299136    B    [/proc/meminfo]
  system.boot_time           2026-11-17T00:40:42Z      [/proc/stat btime]
  storage.home.used_percent  56.2           %    [statfs /home]
  storage.home.free_bytes    868542189568   B    [statfs /home]
  battery.level              100            %    [power_supply BAT0]
  battery.charging           false               [power_supply BAT0]
  battery.status             full                [power_supply BAT0]
  battery.power_w            0              W    [power_supply BAT0]
  power.ac_online            true                [power_supply]
  thermal.cpu_c              53             °C   [hwmon coretemp]
  fan.rpm                    2194           rpm  [hwmon alienware_wmi]
  gpu.busy                   14             %    [nvidia-smi]
  thermal.gpu_c              53.6           °C   [nvidia-smi]
  session.mode               desktop             [process scan]
exit=0
```

Raw sampling while the game ran (about 2 s apart):

```
$ nvidia-smi --query-gpu=utilization.gpu,temperature.gpu --format=csv,noheader,nounits   (x5)
18:59:45 17, 54.28
18:59:47 31, 54.66
18:59:49 16, 54.66
18:59:51 17, 54.78
18:59:53 21, 54.94

$ for each hwmon: fan*_input
alienware_wmi fan1_input = 2134
alienware_wmi fan2_input = 2174
dell_ddv fan1_input = 2134
dell_ddv fan2_input = 2194
dell_smm fan1_input = 2134
dell_smm fan2_input = 2174
```

Fan readings taken just before the load run, at idle:

```
alienware_wmi fan1_input = 2226 / fan2_input = 2262
dell_ddv      fan1_input = 2226 / fan2_input = 2262
dell_smm      fan1_input = 2226 / fan2_input = 2262
```

## Findings

| # | Finding | Impact |
|---|---|---|
| 1 | The fans **do** report non-zero RPM (about 2,100–2,300) at idle and under load, and all three drivers agree. The 0 RPM in the hardware capture was the fans being stopped about 9 minutes after boot, not a broken sensor. | `telemetry.fan` from `alienware_wmi` is valid. Consider moving it from `research` to verified for G15. |
| 2 | `fan.rpm` is a single value, but the G15 has 2 fans. Samples were taken seconds apart, so it is not established which fan it reports (or whether it is an aggregate). | Consider exposing per-fan values or documenting which fan is reported. |
| 3 | `gpu.busy` and `thermal.gpu_c` come from nvidia-smi, rise with load (5–10 % idle, 14–31 % in game), and decimal temperatures parse correctly (54.2, 53.6). This confirms the 673ce5d fix. | NVIDIA path verified on real hardware. |
| 4 | The game ran on the dGPU in Desktop Mode. | Using nvidia-smi as the GPU source is the right choice for gaming load. |
| 5 | `session.mode` = `desktop` stays correct with a game running (heuristic confidence). | OK. `game.current` is not implemented yet (M6), so the running game is not reported. |
| 6 | `battery.power_w` = 0 W with `CURRENT_NOW=1000` µA on AC and a full battery. | Plausible. It has not been checked on battery discharge. |
| 7 | All lifecycle capabilities are available as user `none` privilege. `service.start_at_boot` is disabled because lingering is off. No polkit prompt appeared during probe. | Matches the policy found in the hardware capture. |
| 8 | **`internal/lifecycle` tests fail to build with the default `CGO_ENABLED=1` on SteamOS** (`gcc` has no `cc1`). All of them pass with `CGO_ENABLED=0`. | Developer-experience bug. Set `CGO_ENABLED=0` in local test docs or scripts, or remove the cgo dependency from the test. |
| 9 | `cpu.usage` varies between samples (21.9 % text probe, 8.2 % JSON probe). Each probe takes a short window. | Expected. Not a bug. |

Leftovers from this task, outside the repo:
- `~/.local/go` (Go 1.26.11)
- `~/go/pkg/mod` (module cache)
- `~/.local/share/deckbeacon/bin/deckbeacon`
- `~/.local/state/deckbeacon/identity/`

All of them can be removed with `rm -rf` if wanted. No system files were changed.
