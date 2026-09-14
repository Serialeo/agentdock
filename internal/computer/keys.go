package computer

import (
	"fmt"
	protocol "github.com/Serialeo/agentdock-protocol"
	"strconv"
	"strings"
)

func normalizeKeys(input []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	aliases := map[string]string{"ctrl": "control", "ctl": "control", "⌃": "control", "option": "alt", "opt": "alt", "⌥": "alt", "cmd": "meta", "command": "meta", "super": "meta", "win": "meta", "windows": "meta", "⌘": "meta", "⇧": "shift", "mod": "primary", "commandorcontrol": "primary", "return": "enter", "escape": "esc", "arrowleft": "left", "leftarrow": "left", "arrowright": "right", "rightarrow": "right", "arrowup": "up", "uparrow": "up", "arrowdown": "down", "downarrow": "down", "pgup": "pageup", "pgdn": "pagedown", "spacebar": "space", "back": "backspace", "del": "delete", "ins": "insert", "plus": "+"}
	for _, item := range input {
		parts := []string{item}
		if strings.Contains(item, "+") && item != "+" {
			parts = strings.Split(item, "+")
		}
		for _, part := range parts {
			name := strings.ToLower(strings.TrimSpace(part))
			if len([]rune(name)) > 1 {
				name = strings.NewReplacer("-", "", "_", "", " ", "").Replace(name)
			}
			if alias, ok := aliases[name]; ok {
				name = alias
			}
			if name == "" {
				return nil, failure(protocol.ErrorComputerInputRejected, "empty shortcut key; use Plus for the + key")
			}
			if !seen[name] {
				out = append(out, name)
				seen[name] = true
			}
		}
	}
	if len(out) == 0 {
		return nil, failure(protocol.ErrorComputerInputRejected, "shortcut requires keys")
	}
	return out, nil
}
func namedKey(name, platform string) (keyCode, bool) {
	if name == "primary" {
		name = "control"
		if platform == "darwin" {
			name = "meta"
		}
	}
	mods := map[string]uint64{"shift": modShift, "control": modControl, "alt": modAlt, "meta": modMeta, "fn": modFn}
	win := map[string]uint16{"shift": 0x10, "control": 0x11, "alt": 0x12, "meta": 0x5B, "enter": 0x0D, "tab": 0x09, "esc": 0x1B, "space": 0x20, "backspace": 0x08, "delete": 0x2E, "insert": 0x2D, "home": 0x24, "end": 0x23, "pageup": 0x21, "pagedown": 0x22, "left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28, "capslock": 0x14, "printscreen": 0x2C, "pause": 0x13, "numlock": 0x90, "scrolllock": 0x91}
	mac := map[string]uint16{"shift": 56, "control": 59, "alt": 58, "meta": 55, "fn": 63, "enter": 36, "tab": 48, "esc": 53, "space": 49, "backspace": 51, "delete": 117, "insert": 114, "home": 115, "end": 119, "pageup": 116, "pagedown": 121, "left": 123, "right": 124, "down": 125, "up": 126, "capslock": 57}
	values := win
	if platform == "darwin" {
		values = mac
	}
	if code, ok := values[name]; ok {
		return keyCode{Code: code, Modifier: mods[name], Extended: platform == "windows" && (code >= 0x21 && code <= 0x2E || code == 0x5B)}, true
	}
	if strings.HasPrefix(name, "f") {
		number, err := strconv.Atoi(name[1:])
		if err == nil && number >= 1 && number <= 24 {
			if platform == "windows" {
				return keyCode{Code: uint16(0x70 + number - 1)}, true
			}
			codes := []uint16{122, 120, 99, 118, 96, 97, 98, 100, 101, 109, 103, 111, 105, 107, 113, 106, 64, 79, 80, 90}
			if number <= len(codes) {
				return keyCode{Code: codes[number-1]}, true
			}
		}
	}
	return keyCode{}, false
}
func macKey(name string) (keyCode, error) {
	if key, ok := namedKey(name, "darwin"); ok {
		return key, nil
	}
	codes := map[rune]uint16{'a': 0, 's': 1, 'd': 2, 'f': 3, 'h': 4, 'g': 5, 'z': 6, 'x': 7, 'c': 8, 'v': 9, 'b': 11, 'q': 12, 'w': 13, 'e': 14, 'r': 15, 'y': 16, 't': 17, '1': 18, '2': 19, '3': 20, '4': 21, '6': 22, '5': 23, '=': 24, '9': 25, '7': 26, '-': 27, '8': 28, '0': 29, ']': 30, 'o': 31, 'u': 32, '[': 33, 'i': 34, 'p': 35, 'l': 37, 'j': 38, '\'': 39, 'k': 40, ';': 41, '\\': 42, ',': 43, '/': 44, 'n': 45, 'm': 46, '.': 47, '`': 50}
	runes := []rune(name)
	if len(runes) == 1 {
		r := runes[0]
		shifted := "~!@#$%^&*()_+{}|:\"<>?"
		base := "`1234567890-=[]\\;',./"
		shift := false
		for i, c := range shifted {
			if c == r {
				r = []rune(base)[i]
				shift = true
				break
			}
		}
		if code, ok := codes[r]; ok {
			return keyCode{Code: code, Shift: shift}, nil
		}
	}
	return keyCode{}, failure(protocol.ErrorComputerInputRejected, fmt.Sprintf("unknown shortcut key %q; use text for Unicode text", name))
}
