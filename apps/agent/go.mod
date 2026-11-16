module github.com/jafesu/deckbeacon/apps/agent

go 1.26.0

require (
	github.com/coder/websocket v1.8.14
	github.com/jafesu/deckbeacon/packages/protocol v0.0.0
	go.etcd.io/bbolt v1.4.3
	golang.org/x/net v0.62.0
)

require (
	github.com/flynn/noise v1.1.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	golang.org/x/crypto v0.59.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
)

replace github.com/jafesu/deckbeacon/packages/protocol => ../../packages/protocol
