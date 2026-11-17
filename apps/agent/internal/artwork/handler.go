package artwork

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/jafesu/deckbeacon/apps/agent/internal/dispatch"
)

// Handler implements get_artwork (protocol 1.4): the chunks travel as
// `artwork` attachments ahead of the confirmed result.
func (s *Source) Handler() dispatch.Handler {
	return func(ctx context.Context, cmd dispatch.Command) (dispatch.Result, func(context.Context) dispatch.Result) {
		var args struct {
			AppID uint32 `json:"app_id"`
			Kind  string `json:"kind"`
		}
		raw := cmd.Args
		if len(raw) == 0 {
			raw = json.RawMessage("{}")
		}
		kind, ok := Kind(""), false
		if err := json.Unmarshal(raw, &args); err == nil && args.AppID != 0 {
			kind, ok = ParseKind(args.Kind)
		}
		if !ok {
			return dispatch.Result{CommandID: cmd.CommandID, State: "rejected", Error: &dispatch.Error{Code: "invalid_args", Message: "app_id and kind (capsule|header|hero|logo) required"}}, nil
		}
		img, err := s.Get(args.AppID, kind)
		switch {
		case errors.Is(err, ErrNotFound):
			return dispatch.Result{CommandID: cmd.CommandID, State: "rejected", Error: &dispatch.Error{Code: "unavailable", Message: "no " + string(kind) + " artwork for this app"}}, nil
		case err != nil:
			return dispatch.Result{CommandID: cmd.CommandID, State: "failed", Error: &dispatch.Error{Code: "internal", Message: err.Error()}}, nil
		}
		chunks := img.Chunks()
		atts := make([]dispatch.Attachment, len(chunks))
		for i, c := range chunks {
			atts[i] = dispatch.Attachment{Type: "artwork", Body: map[string]any{
				"command_id": cmd.CommandID, "app_id": args.AppID, "kind": string(kind), "mime": img.MIME,
				"width": img.Width, "height": img.Height, "seq": i, "total": len(chunks),
				"sha256": img.SHA256, "data": base64.StdEncoding.EncodeToString(c),
			}}
		}
		return dispatch.Result{CommandID: cmd.CommandID, State: "confirmed", Attachments: atts,
			Result: map[string]any{"app_id": args.AppID, "kind": string(kind), "bytes": len(img.Data), "sha256": img.SHA256, "mime": img.MIME}}, nil
	}
}
