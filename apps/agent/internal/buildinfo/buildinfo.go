// Package buildinfo carries version metadata injected at link time:
//
//	go build -ldflags "-X github.com/jafesu/deckbeacon/apps/agent/internal/buildinfo.Version=0.1.0 \
//	  -X github.com/jafesu/deckbeacon/apps/agent/internal/buildinfo.Commit=$(git rev-parse --short HEAD)"
package buildinfo

import (
	"fmt"
	"runtime"

	"github.com/jafesu/deckbeacon/packages/protocol"
)

// Set via -ldflags; defaults identify a development build.
var (
	Version = "0.0.0-dev"
	Commit  = "unknown"
)

// Info is the machine-readable build description.
type Info struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Protocol int    `json:"protocol"`
	GoOS     string `json:"goos"`
	GoArch   string `json:"goarch"`
}

// Get returns the current build info.
func Get() Info {
	return Info{Version: Version, Commit: Commit, Protocol: protocol.Version, GoOS: runtime.GOOS, GoArch: runtime.GOARCH}
}

// String renders a one-line human summary.
func (i Info) String() string {
	return fmt.Sprintf("deckbeacon-agent %s (commit %s, protocol v%d, %s/%s)", i.Version, i.Commit, i.Protocol, i.GoOS, i.GoArch)
}
