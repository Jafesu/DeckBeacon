package netwatch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const routeWifi = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\nwlan0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\nwlan0\t0001A8C0\t00000000\t0001\t0\t0\t600\t00FFFFFF\t0\t0\t0\n"
const routeHotspot = "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\nwlan0\t00000000\t012BA8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n"

func TestFingerprintAndWatch(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "net"), 0o755)
	write := func(s string) { _ = os.WriteFile(filepath.Join(root, "net", "route"), []byte(s), 0o644) }
	var mu sync.Mutex
	addrs := []string{"192.168.1.50"}
	get := func() ([]string, error) { mu.Lock(); defer mu.Unlock(); return addrs, nil }
	set := func(a []string) { mu.Lock(); addrs = a; mu.Unlock() }
	write(routeWifi)
	a := Fingerprint(root, get)
	if a != "addr:192.168.1.50|route:wlan0:0101A8C0" {
		t.Fatalf("fingerprint %q (non-default routes must be ignored)", a)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := Watch(ctx, root, 20*time.Millisecond, get)
	select {
	case <-ch:
		t.Fatal("no change, no signal")
	case <-time.After(100 * time.Millisecond):
	}
	write(routeHotspot) // joined a phone hotspot: new gateway
	set([]string{"192.168.43.7"})
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("network change not signalled")
	}
	write("Iface\tDestination\n") // Wi-Fi gone: no default route
	set(nil)
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("loss of network not signalled")
	}
}
