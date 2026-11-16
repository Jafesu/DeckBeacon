module github.com/jafesu/deckbeacon/apps/agent

go 1.26.0

require github.com/jafesu/deckbeacon/packages/protocol v0.0.0

require (
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/jafesu/deckbeacon/packages/protocol => ../../packages/protocol
