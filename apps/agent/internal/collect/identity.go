package collect

import (
	"errors"
	"strings"
)

// OSRelease is the parsed /etc/os-release.
type OSRelease map[string]string

// ReadOSRelease parses /etc/os-release (falling back to /usr/lib/os-release).
func ReadOSRelease(e Env) (OSRelease, error) {
	s, err := e.read("/etc/os-release")
	if err != nil {
		if s, err = e.read("/usr/lib/os-release"); err != nil {
			return nil, err
		}
	}
	out := OSRelease{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		out[k] = strings.Trim(v, `"'`)
	}
	return out, nil
}

// ErrNotSteamOS is returned when the host is not genuine SteamOS (decision D5).
var ErrNotSteamOS = errors.New("this host is not SteamOS (os-release ID != steamos); DeckBeacon supports SteamOS only")

// RequireSteamOS enforces the SteamOS-only policy.
func RequireSteamOS(e Env) (OSRelease, error) {
	osr, err := ReadOSRelease(e)
	if err != nil {
		return nil, err
	}
	if osr["ID"] != "steamos" {
		return osr, ErrNotSteamOS
	}
	return osr, nil
}

// DeviceInfo describes the hardware for display only (never for behaviour).
type DeviceInfo struct {
	Vendor string
	Model  string
	Board  string
}

// ReadDeviceInfo reads DMI identity. Known Valve boards get friendly names.
func ReadDeviceInfo(e Env) DeviceInfo {
	vendor, _ := e.read("/sys/class/dmi/id/sys_vendor")
	board, _ := e.read("/sys/class/dmi/id/board_name")
	product, _ := e.read("/sys/class/dmi/id/product_name")
	model := product
	if vendor == "Valve" {
		switch board {
		case "Jupiter":
			model = "Steam Deck LCD"
		case "Galileo":
			model = "Steam Deck OLED"
		}
	}
	if model == "" {
		model = "SteamOS device"
	}
	return DeviceInfo{Vendor: vendor, Model: model, Board: board}
}
