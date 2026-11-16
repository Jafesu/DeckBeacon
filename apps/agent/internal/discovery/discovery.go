// Package discovery finds Home Assistant on the LAN via mDNS (_home-assistant._tcp).
// It is a one-shot browser using RFC 6762 §6.7 "legacy unicast": the query is sent
// from an ephemeral port, so responders answer directly to us and no listener on
// 5353 is needed (no conflict with avahi). Discovery only proposes URLs; trust
// comes exclusively from SAS pairing (threat model T2).
package discovery

import (
	"context"
	"errors"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Service is the HA mDNS service type.
const Service = "_home-assistant._tcp.local."

// Instance is one discovered HA.
type Instance struct {
	Name    string
	URL     string // best URL: internal_url, else base_url, else host:port
	Version string
}

// MulticastAddr is the mDNS group.
var MulticastAddr = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// Browse queries target (MulticastAddr in production) and collects answers until timeout.
func Browse(ctx context.Context, target *net.UDPAddr, timeout time.Duration) ([]Instance, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	q, err := buildQuery()
	if err != nil {
		return nil, err
	}
	if _, err := conn.WriteToUDP(q, target); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	found := map[string]Instance{}
	buf := make([]byte, 9000)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				break
			}
			return nil, err
		}
		for _, inst := range Parse(buf[:n]) {
			found[inst.URL] = inst
		}
	}
	out := make([]Instance, 0, len(found))
	for _, v := range found {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

func buildQuery() ([]byte, error) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	name, err := dnsmessage.NewName(Service)
	if err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	return b.Finish()
}

// Parse extracts HA instances from one mDNS response (answers + additionals).
func Parse(packet []byte) []Instance {
	var p dnsmessage.Parser
	if _, err := p.Start(packet); err != nil {
		return nil
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil
	}
	var rrs []dnsmessage.Resource
	for _, section := range []func() ([]dnsmessage.Resource, error){p.AllAnswers, p.AllAuthorities, p.AllAdditionals} {
		r, err := section()
		if err != nil {
			break
		}
		rrs = append(rrs, r...)
	}
	instances := map[string]bool{}
	txt := map[string]map[string]string{}
	srv := map[string]*dnsmessage.SRVResource{}
	addrs := map[string]net.IP{}
	for _, rr := range rrs {
		name := strings.ToLower(rr.Header.Name.String())
		switch b := rr.Body.(type) {
		case *dnsmessage.PTRResource:
			if name == Service {
				instances[strings.ToLower(b.PTR.String())] = true
			}
		case *dnsmessage.TXTResource:
			kv := map[string]string{}
			for _, s := range b.TXT {
				k, v, _ := strings.Cut(s, "=")
				kv[strings.ToLower(k)] = v
			}
			txt[name] = kv
		case *dnsmessage.SRVResource:
			srv[name] = b
		case *dnsmessage.AResource:
			addrs[strings.ToLower(rr.Header.Name.String())] = net.IP(b.A[:])
		}
	}
	var out []Instance
	for inst := range instances {
		kv := txt[inst]
		u := firstValidURL(kv["internal_url"], kv["base_url"])
		if u == "" {
			if s := srv[inst]; s != nil {
				host := strings.TrimSuffix(s.Target.String(), ".")
				if ip, ok := addrs[strings.ToLower(s.Target.String())]; ok {
					host = ip.String()
				}
				u = "http://" + net.JoinHostPort(host, strconv.Itoa(int(s.Port)))
			}
		}
		if u == "" {
			continue
		}
		label := strings.TrimSuffix(strings.TrimSuffix(inst, "."+Service), ".")
		if ln := kv["location_name"]; ln != "" {
			label = ln
		}
		out = append(out, Instance{Name: label, URL: u, Version: kv["version"]})
	}
	return out
}

func firstValidURL(cands ...string) string {
	for _, c := range cands {
		if c == "" {
			continue
		}
		if u, err := url.Parse(c); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			return strings.TrimRight(c, "/")
		}
	}
	return ""
}
