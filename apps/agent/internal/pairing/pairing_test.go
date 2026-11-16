package pairing

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/jafesu/deckbeacon/apps/agent/internal/identity"
	"github.com/jafesu/deckbeacon/apps/agent/internal/testutil/fakeha"
)

func req(id *identity.Identity) Request {
	return Request{AgentVersion: "0.1.0", Protocol: map[string]int{"min": 1, "max": 1}, Name: "deck",
		Device: map[string]any{"device_id": id.DeviceID, "os": map[string]any{"id": "steamos", "version_id": "3.7.0"}, "arch": "x86_64"}}
}

func TestPairSuccessBothSidesSeeSameSAS(t *testing.T) {
	srv := fakeha.New()
	defer srv.Close()
	srv.OpenWindow()
	id, _ := identity.LoadOrCreate(t.TempDir())
	var agentSAS string
	tr, err := Pair(context.Background(), srv.URL(), id, req(id), func(ctx context.Context, sas string) (bool, error) {
		agentSAS = sas
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if serverSAS := <-srv.ConfirmSAS; serverSAS != agentSAS || len(agentSAS) != 6 {
		t.Fatalf("SAS mismatch: agent %q server %q", agentSAS, serverSAS)
	}
	if tr.ServerStatic != hex.EncodeToString(srv.Static.Public) {
		t.Fatal("pinned key is not the server's static key")
	}
	if tr.URLs[0].Kind != "manual" || tr.URLs[0].URL != srv.URL() {
		t.Fatalf("urls %+v", tr.URLs)
	}
	if !srv.Paired(hex.EncodeToString(id.Channel.Public)) {
		t.Fatal("server did not enrol device")
	}
	if len(srv.Invalid) != 0 {
		t.Fatalf("agent sent schema-invalid messages: %v", srv.Invalid)
	}
}

func TestNoWindowIs404(t *testing.T) {
	srv := fakeha.New()
	defer srv.Close()
	id, _ := identity.LoadOrCreate(t.TempDir())
	_, err := Pair(context.Background(), srv.URL(), id, req(id), func(context.Context, string) (bool, error) { return true, nil })
	if !errors.Is(err, ErrNoWindow) {
		t.Fatalf("want ErrNoWindow, got %v", err)
	}
}

func TestLocalRejection(t *testing.T) {
	srv := fakeha.New()
	defer srv.Close()
	srv.OpenWindow()
	id, _ := identity.LoadOrCreate(t.TempDir())
	_, err := Pair(context.Background(), srv.URL(), id, req(id), func(context.Context, string) (bool, error) { return false, nil })
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if srv.Paired(hex.EncodeToString(id.Channel.Public)) {
		t.Fatal("device enrolled despite local rejection")
	}
}

func TestServerRejection(t *testing.T) {
	srv := fakeha.New()
	defer srv.Close()
	srv.AutoConfirm = false
	srv.OpenWindow()
	id, _ := identity.LoadOrCreate(t.TempDir())
	_, err := Pair(context.Background(), srv.URL(), id, req(id), func(context.Context, string) (bool, error) { return true, nil })
	var ab *AbortError
	if !errors.As(err, &ab) || ab.Reason != "sas_rejected" {
		t.Fatalf("want abort sas_rejected, got %v", err)
	}
}
