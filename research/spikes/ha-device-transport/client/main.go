// Milestone 0 spike client: NOT PRODUCTION CODE.
// Connects to the spike HA endpoint, authenticates with an Ed25519 device key,
// sends telemetry, acknowledges a pushed command (twice, to exercise dedupe).
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

var context0 = []byte("deckbeacon-spike-v0\x00")

func main() {
	url := flag.String("url", "ws://127.0.0.1:8123/api/deckbeacon_spike/ws", "")
	keyFile := flag.String("key", "", "base64 ed25519 seed")
	devID := flag.String("device", "deck-test-1", "")
	flag.Parse()
	seedB64, err := os.ReadFile(*keyFile)
	check(err)
	seed, err := base64.StdEncoding.DecodeString(string(seedB64))
	check(err)
	priv := ed25519.NewKeyFromSeed(seed)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, *url, nil)
	check(err)
	defer c.CloseNow()

	var ch map[string]any
	check(wsjson.Read(ctx, c, &ch))
	nonceStr, ok := ch["nonce"].(string)
	if !ok {
		fmt.Println("no device challenge; server sent:", ch["type"])
		os.Exit(4)
	}
	nonce, _ := base64.StdEncoding.DecodeString(nonceStr)
	msg := append(append(append([]byte{}, context0...), nonce...), []byte(*devID)...)
	sig := ed25519.Sign(priv, msg)
	check(wsjson.Write(ctx, c, map[string]any{"type": "auth", "device_id": *devID, "sig": base64.StdEncoding.EncodeToString(sig)}))

	var res map[string]any
	check(wsjson.Read(ctx, c, &res))
	fmt.Println("auth result:", res["type"])
	if res["type"] != "auth_ok" {
		os.Exit(3)
	}
	var cmd map[string]any
	check(wsjson.Read(ctx, c, &cmd))
	fmt.Println("received command:", cmd["command_id"], cmd["name"])
	check(wsjson.Write(ctx, c, map[string]any{"type": "telemetry", "seq": 1, "cpu_percent": 42.5}))
	ack := map[string]any{"type": "command_ack", "command_id": cmd["command_id"]}
	check(wsjson.Write(ctx, c, ack))
	check(wsjson.Write(ctx, c, ack)) // replay
	var dup map[string]any
	check(wsjson.Read(ctx, c, &dup))
	b, _ := json.Marshal(dup)
	fmt.Println("replay response:", string(b))
	c.Close(websocket.StatusNormalClosure, "done")
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}
