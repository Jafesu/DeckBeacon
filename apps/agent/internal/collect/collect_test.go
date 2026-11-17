package collect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testEnv(t *testing.T, root string, nvidia string) Env {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "roots", root))
	if err != nil {
		t.Fatal(err)
	}
	return Env{
		Root: abs,
		Now:  func() time.Time { return time.Date(2026, 11, 16, 18, 0, 0, 0, time.UTC) },
		Statfs: func(path string) (uint64, uint64, error) {
			if strings.HasSuffix(path, "/home") {
				return 512e9, 128e9, nil
			}
			return 0, 0, errors.New("no such mount")
		},
		LookPath: func(f string) (string, error) {
			if nvidia != "" && f == "nvidia-smi" {
				return "/usr/bin/nvidia-smi", nil
			}
			return "", errors.New("not found")
		},
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) { return []byte(nvidia), nil },
	}
}

func byKey(ms []Metric) map[string]Metric {
	out := map[string]Metric{}
	for _, m := range ms {
		out[m.Key] = m
	}
	return out
}

func TestSteamDeckOLED(t *testing.T) {
	e := testEnv(t, "steamdeck-oled", "")
	info := ReadDeviceInfo(e)
	if info.Model != "Steam Deck OLED" || info.Vendor != "Valve" {
		t.Fatalf("info %+v", info)
	}
	if _, err := RequireSteamOS(e); err != nil {
		t.Fatal(err)
	}
	s := NewSystem(e)
	m := byKey(s.Collect(context.Background()))
	want := map[string]any{
		"memory.used_percent": 37.2, "battery.level": 76, "battery.charging": false, "battery.status": "discharging",
		"battery.power_w": 12.2, "power.ac_online": false, "thermal.cpu_c": 64.3, "thermal.gpu_c": 61.0,
		"fan.rpm": 3150, "gpu.busy": 42, "session.mode": "gaming", "storage.home.used_percent": 75.0,
		"system.boot_time": "2026-11-16T14:40:00Z",
	}
	for k, v := range want {
		got, ok := m[k]
		if !ok {
			t.Errorf("missing %s", k)
			continue
		}
		if got.Value != v {
			t.Errorf("%s = %v (%T), want %v (%T)", k, got.Value, got.Value, v, v)
		}
	}
	if cpu := m["cpu.usage"]; cpu.Value != nil || cpu.Reason != "warming up" {
		t.Errorf("first cpu sample should be null/warming up, got %+v", cpu)
	}
	if m["battery.level"].Source != "power_supply BAT1" {
		t.Errorf("controller battery must be ignored, source=%s", m["battery.level"].Source)
	}
}

func TestCPUUsageAcrossSamples(t *testing.T) {
	src := testEnv(t, "steamdeck-oled", "")
	dir := t.TempDir()
	// copy tree minimal: only proc/stat needed for this test
	if err := os.MkdirAll(filepath.Join(dir, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "proc", "stat"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src.Root = dir
	s := NewSystem(src)
	write("cpu  100 0 100 800 0 0 0 0 0 0\nbtime 1\n")
	s.Collect(context.Background())
	write("cpu  200 0 200 1400 0 0 0 0 0 0\nbtime 1\n") // +200 busy, +800 total
	m := byKey(s.Collect(context.Background()))
	if m["cpu.usage"].Value != 25.0 {
		t.Fatalf("cpu.usage = %v, want 25", m["cpu.usage"].Value)
	}
}

func TestDellG15WithNvidia(t *testing.T) {
	e := testEnv(t, "dell-g15", "37, 66\n")
	info := ReadDeviceInfo(e)
	if info.Model != "Dell G15 5520" {
		t.Fatalf("model %q", info.Model)
	}
	m := byKey(NewSystem(e).Collect(context.Background()))
	want := map[string]any{
		"thermal.cpu_c": 71.0, "fan.rpm": 2650, "gpu.busy": 37, "thermal.gpu_c": 66.0, "battery.level": 100,
		"battery.status": "full", "battery.power_w": 0.0, "power.ac_online": true, "session.mode": "desktop",
		"memory.used_percent": 75.0,
	}
	for k, v := range want {
		if m[k].Value != v {
			t.Errorf("%s = %v (%T), want %v (%T)", k, m[k].Value, m[k].Value, v, v)
		}
	}
	if m["gpu.busy"].Source != "nvidia-smi" {
		t.Errorf("gpu source %s", m["gpu.busy"].Source)
	}
}

func TestDellG15WithoutNvidiaDriverReportsUnavailable(t *testing.T) {
	e := testEnv(t, "dell-g15", "")
	s := NewSystem(e)
	m := byKey(s.Collect(context.Background()))
	if m["gpu.busy"].Value != nil || m["gpu.busy"].Reason == "" {
		t.Fatalf("gpu.busy must be null with a reason, got %+v", m["gpu.busy"])
	}
	if m["thermal.gpu_c"].Value != nil {
		t.Fatalf("gpu temp must be null, got %+v", m["thermal.gpu_c"])
	}
	caps := map[string]Capability{}
	for _, c := range s.Capabilities(context.Background()) {
		caps[c.ID] = c
	}
	if caps["telemetry.gpu.busy"].Status != "unavailable" || caps["telemetry.cpu"].Status != "available" {
		t.Fatalf("caps %+v %+v", caps["telemetry.gpu.busy"], caps["telemetry.cpu"])
	}
	if caps["power.wake"].Status != "unsupported" || caps["game.launch"].Status != "unsupported" {
		t.Fatal("unimplemented capabilities must be reported unsupported")
	}
}

func TestNotSteamOSRefused(t *testing.T) {
	if _, err := RequireSteamOS(testEnv(t, "not-steamos", "")); !errors.Is(err, ErrNotSteamOS) {
		t.Fatalf("want ErrNotSteamOS, got %v", err)
	}
}

func TestEmptyRootDegradesGracefully(t *testing.T) {
	e := testEnv(t, "steamdeck-oled", "")
	e.Root = t.TempDir()
	m := byKey(NewSystem(e).Collect(context.Background()))
	if _, ok := m["battery.level"]; ok {
		t.Fatal("no battery dir should mean no battery metrics")
	}
	if m["thermal.cpu_c"].Value != nil || m["session.mode"].Value != "none" {
		t.Fatalf("unexpected %+v %+v", m["thermal.cpu_c"], m["session.mode"])
	}
}

// TestDellG15RecordedCapture uses values recorded on the real G15 (2026-11-17).
func TestDellG15RecordedCapture(t *testing.T) {
	e := testEnv(t, "dell-g15-5520", "21, 49.62\n")
	if info := ReadDeviceInfo(e); info.Model != "Dell G15 5520" || info.Vendor != "Dell Inc." {
		t.Fatalf("info %+v (os-release says VARIANT_ID=steamdeck; DMI must win)", info)
	}
	m := byKey(NewSystem(e).Collect(context.Background()))
	want := map[string]any{
		"thermal.cpu_c": 59.0, "gpu.busy": 21, "thermal.gpu_c": 49.6, "fan.rpm": 0, "battery.level": 100,
		"battery.status": "full", "battery.power_w": 0.0, "power.ac_online": true, "session.mode": "desktop",
		"memory.used_percent": 24.7, "system.boot_time": "2026-11-17T00:40:42Z",
	}
	for k, v := range want {
		if m[k].Value != v {
			t.Errorf("%s = %v (%T), want %v (%T) [source %s]", k, m[k].Value, m[k].Value, v, v, m[k].Source)
		}
	}
	if m["gpu.busy"].Source != "nvidia-smi" {
		t.Errorf("gpu source %s", m["gpu.busy"].Source)
	}
}

func TestG15WithoutNvidiaToolFallsBackToLabelledGPUSensor(t *testing.T) {
	m := byKey(NewSystem(testEnv(t, "dell-g15-5520", "")).Collect(context.Background()))
	if m["thermal.gpu_c"].Value != 49.0 || m["thermal.gpu_c"].Confidence != "high" {
		t.Fatalf("gpu temp %+v", m["thermal.gpu_c"])
	}
	if m["gpu.busy"].Value != nil {
		t.Fatalf("gpu.busy must be unavailable without nvidia-smi, got %+v", m["gpu.busy"])
	}
}
