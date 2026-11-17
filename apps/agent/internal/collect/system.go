package collect

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type cpuSample struct{ busy, total uint64 }

// System collects OS-level telemetry. Not safe for concurrent Collect calls.
type System struct {
	env      Env
	prevCPU  *cpuSample
	bootTime time.Time // pinned per boot (btime can shift by ~1 s when the clock is adjusted)
}

// PinBootTime fixes the reported boot time for this boot.
func (s *System) PinBootTime(t time.Time) { s.bootTime = t }

// NewSystem builds the system collector.
func NewSystem(env Env) *System { return &System{env: env} }

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// Collect samples every metric once.
func (s *System) Collect(ctx context.Context) []Metric {
	now := s.env.Now().UTC().Truncate(time.Second)
	var out []Metric
	add := func(m Metric) { m.SampledAt = now; out = append(out, m) }

	// CPU usage needs two samples; the first collect reports null.
	if cur, err := s.readCPU(); err == nil {
		if s.prevCPU != nil && cur.total > s.prevCPU.total {
			add(Metric{Key: "cpu.usage", Value: round1(100 * float64(cur.busy-s.prevCPU.busy) / float64(cur.total-s.prevCPU.total)), Unit: "%", Source: "/proc/stat", Confidence: "confirmed"})
		} else {
			add(Metric{Key: "cpu.usage", Value: nil, Unit: "%", Source: "/proc/stat", Reason: "warming up"})
		}
		s.prevCPU = &cur
	}
	if total, avail, err := s.readMem(); err == nil && total > 0 {
		add(Metric{Key: "memory.used_percent", Value: round1(100 * float64(total-avail) / float64(total)), Unit: "%", Source: "/proc/meminfo", Confidence: "confirmed"})
		add(Metric{Key: "memory.total_bytes", Value: total, Unit: "B", Source: "/proc/meminfo", Confidence: "confirmed"})
	}
	if bt, err := s.BootTime(); err == nil {
		add(Metric{Key: "system.boot_time", Value: bt.UTC().Format(time.RFC3339), Source: "/proc/stat btime", Confidence: "confirmed"})
	}
	out = append(out, s.storage(now)...)
	out = append(out, s.battery(now)...)
	out = append(out, s.thermal(ctx, now)...)
	mode, conf := s.SessionMode()
	add(Metric{Key: "session.mode", Value: mode, Source: "process scan", Confidence: conf})
	return out
}

// BootTime returns the kernel boot time (/proc/stat btime).
func (s *System) BootTime() (time.Time, error) {
	if !s.bootTime.IsZero() {
		return s.bootTime, nil
	}
	return s.readBootTime()
}

func (s *System) readCPU() (cpuSample, error) {
	st, err := s.env.read("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	line, _, _ := strings.Cut(st, "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return cpuSample{}, os.ErrInvalid
	}
	var vals []uint64
	for _, x := range f[1:] {
		v, err := strconv.ParseUint(x, 10, 64)
		if err != nil {
			return cpuSample{}, err
		}
		vals = append(vals, v)
	}
	var total uint64
	for i, v := range vals {
		if i >= 8 { // guest/guest_nice are already counted in user/nice
			break
		}
		total += v
	}
	idle := vals[3]
	if len(vals) > 4 {
		idle += vals[4] // iowait
	}
	return cpuSample{busy: total - idle, total: total}, nil
}

func (s *System) readMem() (total, avail uint64, err error) {
	m, err := s.env.read("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(m, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			avail = v * 1024
		}
	}
	return total, avail, nil
}

func (s *System) readBootTime() (time.Time, error) {
	st, err := s.env.read("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	for _, line := range strings.Split(st, "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, err
			}
			return time.Unix(n, 0), nil
		}
	}
	return time.Time{}, os.ErrNotExist
}

func (s *System) storage(now time.Time) []Metric {
	var out []Metric
	emit := func(prefix, path string) {
		total, free, err := s.env.Statfs(s.env.path(path))
		if err != nil || total == 0 {
			out = append(out, Metric{Key: prefix + ".used_percent", Value: nil, Unit: "%", SampledAt: now, Source: "statfs " + path, Reason: "not mounted"})
			return
		}
		out = append(out,
			Metric{Key: prefix + ".used_percent", Value: round1(100 * float64(total-free) / float64(total)), Unit: "%", SampledAt: now, Source: "statfs " + path, Confidence: "confirmed"},
			Metric{Key: prefix + ".free_bytes", Value: free, Unit: "B", SampledAt: now, Source: "statfs " + path, Confidence: "confirmed"},
		)
	}
	emit("storage.home", "/home")
	// Removable media (SD card): first mount under /run/media/<user>/<label>.
	if m := s.env.glob("/run/media/*/*"); len(m) > 0 {
		sort.Strings(m)
		emit("storage.sd", m[0])
	}
	return out
}

func (s *System) batteryDir() string {
	for _, d := range s.env.glob("/sys/class/power_supply/*") {
		if t, _ := s.env.read(d + "/type"); t != "Battery" {
			continue
		}
		if scope, _ := s.env.read(d + "/scope"); scope == "Device" {
			continue // controller / peripheral batteries
		}
		return d
	}
	return ""
}

func (s *System) battery(now time.Time) []Metric {
	d := s.batteryDir()
	if d == "" {
		return nil
	}
	src := "power_supply " + filepath.Base(d)
	out := []Metric{}
	if c, err := s.env.read(d + "/capacity"); err == nil {
		if v, err := strconv.Atoi(c); err == nil {
			out = append(out, Metric{Key: "battery.level", Value: v, Unit: "%", SampledAt: now, Source: src, Confidence: "confirmed"})
		}
	}
	if st, err := s.env.read(d + "/status"); err == nil {
		out = append(out, Metric{Key: "battery.charging", Value: st == "Charging", SampledAt: now, Source: src, Confidence: "confirmed"})
		out = append(out, Metric{Key: "battery.status", Value: strings.ToLower(strings.ReplaceAll(st, " ", "_")), SampledAt: now, Source: src, Confidence: "confirmed"})
	}
	if w, ok := s.batteryPower(d); ok {
		out = append(out, Metric{Key: "battery.power_w", Value: round1(w), Unit: "W", SampledAt: now, Source: src, Confidence: "high"})
	}
	ac := false
	for _, p := range s.env.glob("/sys/class/power_supply/*") {
		if t, _ := s.env.read(p + "/type"); t == "Mains" || t == "USB" {
			if on, _ := s.env.read(p + "/online"); on == "1" {
				ac = true
			}
		}
	}
	out = append(out, Metric{Key: "power.ac_online", Value: ac, SampledAt: now, Source: "power_supply", Confidence: "high"})
	return out
}

func (s *System) batteryPower(d string) (float64, bool) {
	if p, err := s.env.read(d + "/power_now"); err == nil {
		if v, err := strconv.ParseFloat(p, 64); err == nil {
			return v / 1e6, true
		}
	}
	c, err1 := s.env.read(d + "/current_now")
	v, err2 := s.env.read(d + "/voltage_now")
	if err1 != nil || err2 != nil {
		return 0, false
	}
	ci, e1 := strconv.ParseFloat(c, 64)
	vi, e2 := strconv.ParseFloat(v, 64)
	if e1 != nil || e2 != nil {
		return 0, false
	}
	return math.Abs(ci) * vi / 1e12, true
}

type hwmon struct {
	dir, name string
}

func (s *System) hwmons() []hwmon {
	var out []hwmon
	for _, d := range s.env.glob("/sys/class/hwmon/hwmon*") {
		n, _ := s.env.read(d + "/name")
		out = append(out, hwmon{d, n})
	}
	return out
}

func (s *System) temp(h hwmon, preferLabels ...string) (float64, bool) {
	files := s.env.glob(h.dir + "/temp*_input")
	best, found := 0.0, false
	for _, f := range files {
		raw, err := s.env.read(f)
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		c := v / 1000
		label, _ := s.env.read(strings.TrimSuffix(f, "_input") + "_label")
		for _, want := range preferLabels {
			if label == want {
				return c, true
			}
		}
		if !found || c > best {
			best, found = c, true
		}
	}
	return best, found
}

// labelled returns the temperature of the sensor in h whose label is exactly want.
func (s *System) labelled(h hwmon, want string) (float64, bool) {
	for _, f := range s.env.glob(h.dir + "/temp*_label") {
		if l, _ := s.env.read(f); l == want {
			raw, err := s.env.read(strings.TrimSuffix(f, "_label") + "_input")
			if err != nil {
				return 0, false
			}
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return 0, false
			}
			return v / 1000, true
		}
	}
	return 0, false
}

func (s *System) thermal(ctx context.Context, now time.Time) []Metric {
	var out []Metric
	mons := s.hwmons()
	cpuDone, gpuDone := false, false
	maxFan, fanSrc := -1.0, ""
	for _, h := range mons {
		switch h.name {
		case "k10temp", "coretemp", "zenpower":
			if !cpuDone {
				if c, ok := s.temp(h, "Tctl", "Package id 0", "Tdie"); ok {
					out = append(out, Metric{Key: "thermal.cpu_c", Value: round1(c), Unit: "°C", SampledAt: now, Source: "hwmon " + h.name, Confidence: "confirmed"})
					cpuDone = true
				}
			}
		case "amdgpu":
			if !gpuDone {
				if c, ok := s.temp(h, "edge"); ok {
					out = append(out, Metric{Key: "thermal.gpu_c", Value: round1(c), Unit: "°C", SampledAt: now, Source: "hwmon amdgpu", Confidence: "confirmed"})
					gpuDone = true
				}
			}
		}
		for _, f := range s.env.glob(h.dir + "/fan*_input") {
			if raw, err := s.env.read(f); err == nil {
				if v, err := strconv.ParseFloat(raw, 64); err == nil && v > maxFan {
					maxFan, fanSrc = v, "hwmon "+h.name
				}
			}
		}
	}
	if maxFan >= 0 {
		out = append(out, Metric{Key: "fan.rpm", Value: int(maxFan), Unit: "rpm", SampledAt: now, Source: fanSrc, Confidence: "confirmed"})
	}

	// GPU utilisation: prefer the discrete NVIDIA GPU when its tool is present.
	if util, temp, ok := s.nvidia(ctx); ok {
		out = append(out, Metric{Key: "gpu.busy", Value: util, Unit: "%", SampledAt: now, Source: "nvidia-smi", Confidence: "confirmed"})
		if !gpuDone {
			out = append(out, Metric{Key: "thermal.gpu_c", Value: temp, Unit: "°C", SampledAt: now, Source: "nvidia-smi", Confidence: "confirmed"})
			gpuDone = true
		}
	} else if busy := s.env.glob("/sys/class/drm/card*/device/gpu_busy_percent"); len(busy) > 0 {
		sort.Strings(busy)
		if raw, err := s.env.read(busy[0]); err == nil {
			if v, err := strconv.Atoi(raw); err == nil {
				out = append(out, Metric{Key: "gpu.busy", Value: v, Unit: "%", SampledAt: now, Source: "amdgpu gpu_busy_percent", Confidence: "confirmed"})
			}
		}
	} else {
		out = append(out, Metric{Key: "gpu.busy", Value: nil, Unit: "%", SampledAt: now, Source: "none", Reason: "no supported GPU utilisation source (amdgpu sysfs or nvidia-smi)"})
	}
	if !gpuDone {
		for _, h := range mons {
			if c, ok := s.labelled(h, "GPU"); ok {
				out = append(out, Metric{Key: "thermal.gpu_c", Value: round1(c), Unit: "°C", SampledAt: now, Source: "hwmon " + h.name + " (GPU label)", Confidence: "high"})
				gpuDone = true
				break
			}
		}
	}
	if !gpuDone {
		out = append(out, Metric{Key: "thermal.gpu_c", Value: nil, Unit: "°C", SampledAt: now, Source: "none", Reason: "no GPU temperature source"})
	}
	if !cpuDone {
		out = append(out, Metric{Key: "thermal.cpu_c", Value: nil, Unit: "°C", SampledAt: now, Source: "none", Reason: "no supported CPU temperature sensor"})
	}
	return out
}

func (s *System) nvidia(ctx context.Context) (util int, temp float64, ok bool) {
	if s.env.LookPath == nil || s.env.Run == nil {
		return 0, 0, false
	}
	bin, err := s.env.LookPath("nvidia-smi")
	if err != nil {
		return 0, 0, false
	}
	raw, err := s.env.Run(ctx, bin, "--query-gpu=utilization.gpu,temperature.gpu", "--format=csv,noheader,nounits")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(raw)), "\n")
	parts := strings.Split(line, ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	// Recorded on a Dell G15 (driver 2026): temperature prints with decimals ("49.62").
	u, e1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	t, e2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if e1 != nil || e2 != nil {
		return 0, 0, false
	}
	return int(math.Round(u)), round1(t), true
}

// SessionMode detects Gaming vs Desktop mode from running processes (heuristic).
func (s *System) SessionMode() (mode, confidence string) {
	gaming, desktop := false, false
	for _, c := range s.env.glob("/proc/[0-9]*/comm") {
		name, err := s.env.read(c)
		if err != nil {
			continue
		}
		switch name {
		case "gamescope-session", "gamescope", "gamescope-wl":
			gaming = true
		case "plasmashell", "kwin_wayland", "kwin_x11":
			desktop = true
		}
	}
	switch {
	case gaming && !desktop:
		return "gaming", "heuristic"
	case desktop && !gaming:
		return "desktop", "heuristic"
	case gaming && desktop:
		return "unknown", "low"
	default:
		return "none", "heuristic"
	}
}

// Capabilities reports what this host can provide (spec 02 statuses).
func (s *System) Capabilities(ctx context.Context) []Capability {
	ms := s.Collect(ctx)
	has := map[string]Metric{}
	for _, m := range ms {
		has[m.Key] = m
	}
	cap := func(id, key, source string) Capability {
		m, ok := has[key]
		switch {
		case !ok:
			return Capability{ID: id, Status: "unavailable", Tier: "system", Source: source, Privilege: "none", Reason: "not present on this device"}
		case m.Value == nil && m.Reason != "warming up":
			return Capability{ID: id, Status: "unavailable", Tier: "system", Source: m.Source, Privilege: "none", Reason: m.Reason}
		default:
			return Capability{ID: id, Status: "available", Tier: "system", Source: m.Source, Privilege: "none"}
		}
	}
	out := []Capability{
		cap("telemetry.cpu", "cpu.usage", "/proc/stat"),
		cap("telemetry.memory", "memory.used_percent", "/proc/meminfo"),
		cap("telemetry.storage", "storage.home.used_percent", "statfs"),
		cap("telemetry.battery", "battery.level", "power_supply"),
		cap("telemetry.thermal.cpu", "thermal.cpu_c", "hwmon"),
		cap("telemetry.thermal.gpu", "thermal.gpu_c", "hwmon/nvidia-smi"),
		cap("telemetry.gpu.busy", "gpu.busy", "amdgpu/nvidia-smi"),
		cap("telemetry.fan", "fan.rpm", "hwmon"),
		cap("session.mode", "session.mode", "process scan"),
	}
	planned := func(id, ms string) Capability {
		return Capability{ID: id, Status: "unsupported", Tier: "none", Source: "none", Reason: "not implemented in this agent version (planned " + ms + ")"}
	}
	out = append(out,
		planned("notifications.display", "M7"),
		planned("power.suspend", "M7"), planned("power.reboot", "M7"), planned("power.shutdown", "M7"),
		Capability{ID: "power.wake", Status: "unsupported", Tier: "none", Source: "none", Reason: "wake from sleep/off is not supported (spec 02: never guaranteed)"},
	)
	return out
}
