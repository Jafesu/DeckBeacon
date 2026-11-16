package noise

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite ../vectors/noise.json")

func seed(label string) []byte {
	s := sha256.Sum256([]byte("deckbeacon-test-vector/" + label))
	return s[:]
}

func mustKP(t *testing.T, priv []byte) KeyPair {
	t.Helper()
	kp, err := KeyPairFromPrivate(priv)
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

// pair runs a complete handshake between two in-memory peers.
func pair(t *testing.T, p Pattern, prologue []byte, iStatic, rStatic KeyPair, iRand, rRand []byte, payloads [][]byte) (*Handshake, *Handshake, [][]byte) {
	t.Helper()
	icfg := Config{Pattern: p, Initiator: true, Prologue: prologue, Static: iStatic, Random: FixedRandom(iRand)}
	if p == IK {
		icfg.RemoteStatic = rStatic.Public
	}
	ini, err := NewHandshake(icfg)
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewHandshake(Config{Pattern: p, Initiator: false, Prologue: prologue, Static: rStatic, Random: FixedRandom(rRand)})
	if err != nil {
		t.Fatal(err)
	}
	var msgs [][]byte
	for i := 0; !ini.Done() || !res.Done(); i++ {
		w, r := ini, res
		if !ini.MyTurn() {
			w, r = res, ini
		}
		msg, err := w.WriteMessage(payloads[i])
		if err != nil {
			t.Fatal(err)
		}
		got, err := r.ReadMessage(msg)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payloads[i]) {
			t.Fatalf("payload %d mismatch", i)
		}
		msgs = append(msgs, msg)
	}
	return ini, res, msgs
}

type vecMsg struct {
	Payload    string `json:"payload"`
	Ciphertext string `json:"ciphertext"`
}

type vecTransport struct {
	FromInitiator bool   `json:"from_initiator"`
	Payload       string `json:"payload"`
	Ciphertext    string `json:"ciphertext"`
}

type vector struct {
	Name              string         `json:"name"`
	Pattern           string         `json:"pattern"`
	Prologue          string         `json:"prologue"`
	InitStaticPriv    string         `json:"init_static_private"`
	RespStaticPriv    string         `json:"resp_static_private"`
	InitEphemeralPriv string         `json:"init_ephemeral_private"`
	RespEphemeralPriv string         `json:"resp_ephemeral_private"`
	Handshake         []vecMsg       `json:"handshake"`
	HandshakeHash     string         `json:"handshake_hash"`
	SAS               string         `json:"sas"`
	Transport         []vecTransport `json:"transport"`
	InitStaticPublic  string         `json:"init_static_public"`
	RespStaticPublic  string         `json:"resp_static_public"`
}

func buildVectors(t *testing.T) []vector {
	t.Helper()
	cases := []struct {
		name     string
		p        Pattern
		prologue []byte
		payloads [][]byte
	}{
		{"pair_xx_empty_payloads", XX, ProloguePair, [][]byte{{}, {}, {}}},
		{"pair_xx_with_payloads", XX, ProloguePair, [][]byte{[]byte("hello"), []byte(`{"server":"ha"}`), []byte(`{"device":"deck"}`)}},
		{"session_ik_empty_payloads", IK, PrologueSession, [][]byte{{}, {}}},
		{"session_ik_with_payloads", IK, PrologueSession, [][]byte{[]byte("agent"), []byte("ha")}},
	}
	var out []vector
	for _, c := range cases {
		iS, rS := mustKP(t, seed(c.name+"/is")), mustKP(t, seed(c.name+"/rs"))
		iE, rE := seed(c.name+"/ie"), seed(c.name+"/re")
		ini, res, msgs := pair(t, c.p, c.prologue, iS, rS, iE, rE, c.payloads)
		is, _ := ini.Session()
		rs, _ := res.Session()
		if !bytes.Equal(is.HandshakeHash(), rs.HandshakeHash()) {
			t.Fatal("handshake hash mismatch")
		}
		if !bytes.Equal(is.RemoteStatic(), rS.Public) || !bytes.Equal(rs.RemoteStatic(), iS.Public) {
			t.Fatal("remote static mismatch")
		}
		v := vector{
			Name: c.name, Pattern: c.p.String(), Prologue: hex.EncodeToString(c.prologue),
			InitStaticPriv: hex.EncodeToString(iS.Private), RespStaticPriv: hex.EncodeToString(rS.Private),
			InitStaticPublic: hex.EncodeToString(iS.Public), RespStaticPublic: hex.EncodeToString(rS.Public),
			InitEphemeralPriv: hex.EncodeToString(iE), RespEphemeralPriv: hex.EncodeToString(rE),
			HandshakeHash: hex.EncodeToString(is.HandshakeHash()), SAS: SAS(is.HandshakeHash()),
		}
		for i, m := range msgs {
			v.Handshake = append(v.Handshake, vecMsg{hex.EncodeToString(c.payloads[i]), hex.EncodeToString(m)})
		}
		for i, pl := range []string{"first from initiator", "", `{"v":1,"type":"ping"}`} {
			for _, fromInit := range []bool{true, false} {
				s := is
				if !fromInit {
					s = rs
				}
				payload := []byte(pl + string(rune('a'+i)))
				ct, err := s.Seal(payload)
				if err != nil {
					t.Fatal(err)
				}
				r := rs
				if !fromInit {
					r = is
				}
				back, err := r.Open(ct)
				if err != nil || !bytes.Equal(back, payload) {
					t.Fatalf("transport roundtrip failed: %v", err)
				}
				v.Transport = append(v.Transport, vecTransport{fromInit, hex.EncodeToString(payload), hex.EncodeToString(ct)})
			}
		}
		out = append(out, v)
	}
	return out
}

func TestVectorsAreCurrent(t *testing.T) {
	got, err := json.MarshalIndent(buildVectors(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("..", "vectors", "noise.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run: go test ./noise -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("vectors/noise.json is stale; run: go test ./noise -update")
	}
}

func randomKP(t *testing.T) KeyPair {
	kp, err := GenerateKeyPair(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

func TestIKWrongServerKeyFails(t *testing.T) {
	agent, server, impostor := randomKP(t), randomKP(t), randomKP(t)
	ini, _ := NewHandshake(Config{Pattern: IK, Initiator: true, Prologue: PrologueSession, Static: agent, RemoteStatic: impostor.Public, Random: rand.Reader})
	res, _ := NewHandshake(Config{Pattern: IK, Prologue: PrologueSession, Static: server, Random: rand.Reader})
	m1, err := ini.WriteMessage(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := res.ReadMessage(m1); err == nil {
		t.Fatal("server accepted a message encrypted to another static key")
	}
}

func TestPrologueMismatchFails(t *testing.T) {
	a, b := randomKP(t), randomKP(t)
	ini, _ := NewHandshake(Config{Pattern: XX, Initiator: true, Prologue: ProloguePair, Static: a, Random: rand.Reader})
	res, _ := NewHandshake(Config{Pattern: XX, Prologue: PrologueSession, Static: b, Random: rand.Reader})
	m1, _ := ini.WriteMessage(nil)
	if _, err := res.ReadMessage(m1); err != nil {
		t.Fatal(err) // msg1 of XX is unauthenticated e; failure surfaces at msg2
	}
	m2, _ := res.WriteMessage(nil)
	if _, err := ini.ReadMessage(m2); err == nil {
		t.Fatal("prologue mismatch not detected")
	}
}

func TestTamperAndReplayFail(t *testing.T) {
	ini, res, _ := pair(t, IK, PrologueSession, randomKP(t), randomKP(t), seed("x/ie"), seed("x/re"), [][]byte{{}, {}})
	is, _ := ini.Session()
	rs, _ := res.Session()
	ct, _ := is.Seal([]byte("hello"))
	bad := append([]byte(nil), ct...)
	bad[0] ^= 1
	if _, err := rs.Open(bad); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	// After a failed Open, flynn does not advance the nonce, so the genuine message still opens once.
	if _, err := rs.Open(ct); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Open(ct); err == nil {
		t.Fatal("replayed transport message accepted")
	}
}

func TestSASIsSixDigitsAndDiffersForMITM(t *testing.T) {
	if s := SAS([]byte("a")); len(s) != 6 {
		t.Fatalf("SAS %q", s)
	}
	if SAS([]byte("a")) == SAS([]byte("b")) {
		t.Fatal("SAS collision on trivially different inputs")
	}
}

func TestTurnEnforcement(t *testing.T) {
	ini, _ := NewHandshake(Config{Pattern: XX, Initiator: true, Prologue: ProloguePair, Static: randomKP(t), Random: rand.Reader})
	if _, err := ini.ReadMessage([]byte{}); err == nil {
		t.Fatal("initiator read before writing")
	}
	if _, err := ini.Session(); err == nil {
		t.Fatal("session before completion")
	}
	if _, err := NewHandshake(Config{Pattern: IK, Initiator: true, Static: randomKP(t), Random: rand.Reader}); err == nil {
		t.Fatal("IK initiator without remote static accepted")
	}
}
