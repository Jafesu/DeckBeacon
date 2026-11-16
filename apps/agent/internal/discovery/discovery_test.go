package discovery

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func response(t *testing.T, txt []string, withSRV bool) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	svc := dnsmessage.MustNewName(Service)
	inst := dnsmessage.MustNewName("Home._home-assistant._tcp.local.")
	host := dnsmessage.MustNewName("homeassistant.local.")
	_ = b.StartAnswers()
	_ = b.PTRResource(dnsmessage.ResourceHeader{Name: svc, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.PTRResource{PTR: inst})
	_ = b.StartAdditionals()
	if txt != nil {
		_ = b.TXTResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.TXTResource{TXT: txt})
	}
	if withSRV {
		_ = b.SRVResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.SRVResource{Target: host, Port: 8123})
		_ = b.AResource(dnsmessage.ResourceHeader{Name: host, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.AResource{A: [4]byte{192, 0, 2, 10}})
	}
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParsePrefersInternalURL(t *testing.T) {
	got := Parse(response(t, []string{"location_name=My Home", "base_url=http://192.0.2.10:8123", "internal_url=http://ha.lan:8123/", "version=2026.11.1"}, true))
	if len(got) != 1 || got[0].URL != "http://ha.lan:8123" || got[0].Name != "My Home" || got[0].Version != "2026.11.1" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseFallsBackToSRV(t *testing.T) {
	got := Parse(response(t, []string{"internal_url=javascript:x"}, true))
	if len(got) != 1 || got[0].URL != "http://192.0.2.10:8123" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseGarbage(t *testing.T) {
	if got := Parse([]byte{1, 2, 3}); got != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestBrowseLegacyUnicast(t *testing.T) {
	// A fake responder answering the query unicast, as an mDNS responder does for
	// queries from a non-5353 source port.
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	resp := response(t, []string{"base_url=http://192.0.2.10:8123"}, false)
	go func() {
		buf := make([]byte, 1500)
		n, from, err := srv.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var p dnsmessage.Parser
		if _, err := p.Start(buf[:n]); err != nil {
			return
		}
		if q, err := p.Question(); err == nil && q.Type == dnsmessage.TypePTR {
			_, _ = srv.WriteToUDP(resp, from)
		}
	}()
	got, err := Browse(context.Background(), srv.LocalAddr().(*net.UDPAddr), 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].URL != "http://192.0.2.10:8123" {
		t.Fatalf("got %+v", got)
	}
}
