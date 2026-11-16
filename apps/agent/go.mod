module github.com/jafesu/deckbeacon/apps/agent

go 1.24

require github.com/jafesu/deckbeacon/packages/protocol v0.0.0

require (
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	golang.org/x/text v0.14.0 // indirect
)

replace github.com/jafesu/deckbeacon/packages/protocol => ../../packages/protocol
