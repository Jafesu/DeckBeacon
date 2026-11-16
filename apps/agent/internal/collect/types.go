// Package collect implements capability-based system collectors (spec 03, ADR-0004
// tier "system"). Every path is resolved under Env.Root so tests run against
// recorded or synthetic trees; production uses Root "/".
package collect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Metric is one telemetry sample (protocol telemetry.metrics item).
// Value nil means "not available right now"; Reason explains why. Never cached.
type Metric struct {
	Key        string    `json:"key"`
	Value      any       `json:"value"`
	Unit       string    `json:"unit,omitempty"`
	SampledAt  time.Time `json:"sampled_at"`
	Source     string    `json:"source"`
	Confidence string    `json:"confidence,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

// Capability is one entry of the capability snapshot.
type Capability struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Tier         string `json:"tier,omitempty"`
	Experimental bool   `json:"experimental,omitempty"`
	Source       string `json:"source"`
	Privilege    string `json:"privilege,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// Env abstracts the host so collectors are fully testable.
type Env struct {
	Root     string
	Now      func() time.Time
	Statfs   func(path string) (total, free uint64, err error)
	LookPath func(file string) (string, error)
	Run      func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// HostEnv returns the production environment.
func HostEnv() Env {
	return Env{
		Root: "/",
		Now:  time.Now,
		Statfs: func(path string) (uint64, uint64, error) {
			var st syscall.Statfs_t
			if err := syscall.Statfs(path, &st); err != nil {
				return 0, 0, err
			}
			bs := uint64(st.Bsize)
			return st.Blocks * bs, st.Bavail * bs, nil
		},
		LookPath: exec.LookPath,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, name, args...).Output()
		},
	}
}

func (e Env) path(p string) string { return filepath.Join(e.Root, p) }

func (e Env) read(p string) (string, error) {
	b, err := os.ReadFile(e.path(p))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (e Env) glob(pattern string) []string {
	m, _ := filepath.Glob(e.path(pattern))
	out := make([]string, 0, len(m))
	for _, x := range m {
		rel, err := filepath.Rel(e.Root, x)
		if err == nil {
			out = append(out, "/"+rel)
		}
	}
	return out
}
