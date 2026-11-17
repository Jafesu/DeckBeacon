package activity

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
)

// Standalone mode: no Discord client is running, so the proxy answers the
// game's IPC itself (owner decision 2026-11-17, ADR-0007 amendment). It speaks
// only the documented local RPC subset games use for presence: HANDSHAKE ->
// READY, SET_ACTIVITY (echoed back), SUBSCRIBE/UNSUBSCRIBE (acknowledged),
// PING -> PONG, CLOSE. Anything else gets an RPC error. It never contacts
// Discord's servers and identifies itself as DeckBeacon, not as a Discord user.

// readyPayload is the READY dispatch. The user object is a fixed local
// placeholder named "DeckBeacon": SDKs expect the fields to exist.
var readyPayload = mustJSON(map[string]any{
	"cmd": "DISPATCH", "evt": "READY", "nonce": nil,
	"data": map[string]any{
		"v":      1,
		"config": map[string]any{"cdn_host": "cdn.discordapp.com", "api_endpoint": "//discord.com/api", "environment": "production"},
		"user":   map[string]any{"id": "0", "username": "DeckBeacon", "discriminator": "0", "global_name": "DeckBeacon", "avatar": nil, "bot": false, "flags": 0, "premium_type": 0},
	},
})

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func writeFrame(w io.Writer, op uint32, body []byte) error {
	b := make([]byte, 8+len(body))
	binary.LittleEndian.PutUint32(b[0:], op)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(body)))
	copy(b[8:], body)
	_, err := w.Write(b)
	return err
}

func readFrameRaw(r io.Reader) (uint32, []byte, error) {
	h := make([]byte, 8)
	if _, err := io.ReadFull(r, h); err != nil {
		return 0, nil, err
	}
	n := binary.LittleEndian.Uint32(h[4:])
	if n > maxParsed { // a local presence frame is small; larger is not a game talking RPC
		return 0, nil, io.ErrUnexpectedEOF
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return 0, nil, err
	}
	return binary.LittleEndian.Uint32(h), b, nil
}

func (p *Proxy) serveLocal(id uint64, c net.Conn) {
	op, body, err := readFrameRaw(c)
	if err != nil {
		return
	}
	var hs struct {
		V        json.Number `json:"v"`
		ClientID string      `json:"client_id"`
	}
	if op != opHandshake || json.Unmarshal(body, &hs) != nil || hs.V.String() != "1" || hs.ClientID == "" {
		_ = writeFrame(c, opClose, mustJSON(map[string]any{"code": 4000, "message": "Invalid handshake"}))
		return
	}
	if writeFrame(c, opFrame, readyPayload) != nil {
		return
	}
	for {
		op, body, err := readFrameRaw(c)
		if err != nil {
			return
		}
		switch op {
		case opPing:
			if writeFrame(c, opPong, body) != nil {
				return
			}
		case opClose:
			return
		case opFrame:
			if writeFrame(c, opFrame, p.localReply(id, body)) != nil {
				return
			}
		default:
			return
		}
	}
}

func (p *Proxy) localReply(id uint64, body []byte) []byte {
	var f struct {
		Cmd   string          `json:"cmd"`
		Nonce json.RawMessage `json:"nonce"`
		Evt   string          `json:"evt"`
		Args  struct {
			Activity json.RawMessage `json:"activity"`
		} `json:"args"`
	}
	if json.Unmarshal(body, &f) != nil {
		return mustJSON(map[string]any{"cmd": "DISPATCH", "evt": "ERROR", "nonce": nil, "data": map[string]any{"code": 4002, "message": "Invalid payload"}})
	}
	nonce := any(nil)
	if len(f.Nonce) > 0 {
		nonce = f.Nonce
	}
	switch f.Cmd {
	case "SET_ACTIVITY":
		p.inspect(id, body)
		data := any(nil)
		if len(f.Args.Activity) > 0 {
			data = f.Args.Activity // Discord echoes the activity it applied
		}
		return mustJSON(map[string]any{"cmd": f.Cmd, "evt": nil, "nonce": nonce, "data": data})
	case "SUBSCRIBE", "UNSUBSCRIBE":
		return mustJSON(map[string]any{"cmd": f.Cmd, "evt": nil, "nonce": nonce, "data": map[string]any{"evt": f.Evt}})
	}
	return mustJSON(map[string]any{"cmd": f.Cmd, "evt": "ERROR", "nonce": nonce, "data": map[string]any{"code": 1000, "message": "Not supported by DeckBeacon (Discord is not running)"}})
}
