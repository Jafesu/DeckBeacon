// Package netwatch notices network changes (ADR-0011): a different default
// route or a changed set of interface addresses means the agent should check
// its connection now instead of waiting for a heartbeat timeout. Polling
// /proc/net/route and the interface list needs no privileges or netlink.
package netwatch

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Fingerprint summarises the current network: default routes (interface and
// gateway) and global unicast addresses. Loopback and link-local are ignored.
func Fingerprint(procRoot string, addrs func() ([]string, error)) string {
	var parts []string
	if b, err := os.ReadFile(filepath.Join(procRoot, "net", "route")); err == nil {
		for _, line := range strings.Split(string(b), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) >= 3 && f[1] == "00000000" { // destination 0.0.0.0 = default route
				parts = append(parts, "route:"+f[0]+":"+f[2])
			}
		}
	}
	if as, err := addrs(); err == nil {
		for _, a := range as {
			parts = append(parts, "addr:"+a)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// SystemAddrs lists global unicast interface addresses.
func SystemAddrs() ([]string, error) {
	ifs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range ifs {
		ipn, ok := a.(*net.IPNet)
		if !ok || !ipn.IP.IsGlobalUnicast() {
			continue
		}
		out = append(out, ipn.IP.String())
	}
	return out, nil
}

// Watch sends on the returned channel (coalesced) whenever the fingerprint
// changes. It stops when ctx is done.
func Watch(ctx context.Context, procRoot string, every time.Duration, addrs func() ([]string, error)) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		last := Fingerprint(procRoot, addrs)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fp := Fingerprint(procRoot, addrs)
				if fp != last {
					last = fp
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	return ch
}
