// Package steam reads Steam's local library state (ADR-0004 Tier A: read-only
// files and the process table). It never writes Steam files and never opens
// credential-bearing files (see forbidden).
package steam

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Node is a KeyValues node: either a string value or a child map (ordered keys).
type Node struct {
	Value    string
	Children map[string]*Node
	Order    []string
}

// IsMap reports whether the node holds children.
func (n *Node) IsMap() bool { return n != nil && n.Children != nil }

// Get returns a child by case-insensitive key path.
func (n *Node) Get(path ...string) *Node {
	cur := n
	for _, p := range path {
		if cur == nil || cur.Children == nil {
			return nil
		}
		next, ok := cur.Children[strings.ToLower(p)]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// Str returns the string value at path ("" if absent or a map).
func (n *Node) Str(path ...string) string {
	c := n.Get(path...)
	if c == nil || c.Children != nil {
		return ""
	}
	return c.Value
}

const (
	maxVDFBytes = 8 << 20
	maxDepth    = 32
)

// forbidden lists files DeckBeacon must never open: they hold login tokens or
// account data (threat model T10). Enforced in ReadVDF.
var forbidden = []string{"config.vdf", "localconfig.vdf", "loginusers.vdf"}

// ErrForbidden is returned when asked to read a credential-bearing file.
var ErrForbidden = errors.New("refusing to read a Steam credential file")

func isForbidden(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasPrefix(base, "ssfn") {
		return true
	}
	for _, f := range forbidden {
		if base == f {
			return true
		}
	}
	return false
}

// ReadVDF parses a text KeyValues file, refusing credential files.
func ReadVDF(path string) (*Node, error) {
	if isForbidden(path) {
		return nil, fmt.Errorf("%w: %s", ErrForbidden, filepath.Base(path))
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxVDFBytes {
		return nil, fmt.Errorf("vdf: %s too large", filepath.Base(path))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseVDF(string(b))
}

// ParseVDF parses KeyValues text. Keys are case-insensitive (stored lowercase);
// duplicate keys keep the last value. Conditionals ([$WIN32]) are ignored.
func ParseVDF(s string) (*Node, error) {
	p := &parser{s: s}
	root := &Node{Children: map[string]*Node{}}
	if err := p.parseBody(root, 0, false); err != nil {
		return nil, err
	}
	return root, nil
}

type parser struct {
	s   string
	pos int
}

func (p *parser) skip() {
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			p.pos++
		case c == '/' && p.pos+1 < len(p.s) && p.s[p.pos+1] == '/':
			for p.pos < len(p.s) && p.s[p.pos] != '\n' {
				p.pos++
			}
		case c == '[': // conditional like [$WIN32]
			for p.pos < len(p.s) && p.s[p.pos] != ']' {
				p.pos++
			}
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) token() (string, bool, error) {
	p.skip()
	if p.pos >= len(p.s) {
		return "", false, nil
	}
	if p.s[p.pos] == '"' {
		p.pos++
		var b strings.Builder
		for p.pos < len(p.s) {
			c := p.s[p.pos]
			if c == '\\' && p.pos+1 < len(p.s) {
				p.pos++
				switch p.s[p.pos] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(p.s[p.pos])
				}
				p.pos++
				continue
			}
			if c == '"' {
				p.pos++
				return b.String(), true, nil
			}
			b.WriteByte(c)
			p.pos++
		}
		return "", false, errors.New("vdf: unterminated string")
	}
	start := p.pos
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '{' || c == '}' || c == '"' {
			break
		}
		p.pos++
	}
	return p.s[start:p.pos], true, nil
}

func (p *parser) parseBody(n *Node, depth int, nested bool) error {
	if depth > maxDepth {
		return errors.New("vdf: nesting too deep")
	}
	for {
		p.skip()
		if p.pos >= len(p.s) {
			if nested {
				return errors.New("vdf: unexpected end of input")
			}
			return nil
		}
		if p.s[p.pos] == '}' {
			if !nested {
				return errors.New("vdf: unbalanced '}'")
			}
			p.pos++
			return nil
		}
		key, ok, err := p.token()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("vdf: expected key")
		}
		p.skip()
		if p.pos < len(p.s) && p.s[p.pos] == '{' {
			p.pos++
			child := &Node{Children: map[string]*Node{}}
			if err := p.parseBody(child, depth+1, true); err != nil {
				return err
			}
			n.set(key, child)
			continue
		}
		val, ok, err := p.token()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("vdf: expected value for " + key)
		}
		n.set(key, &Node{Value: val})
	}
}

func (n *Node) set(key string, v *Node) {
	k := strings.ToLower(key)
	if _, exists := n.Children[k]; !exists {
		n.Order = append(n.Order, k)
	}
	n.Children[k] = v
}
