package games

import "encoding/json"

func jsonUnmarshal(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	return json.Unmarshal(raw, v)
}
