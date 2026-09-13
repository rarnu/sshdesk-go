// Package agent implements the low-frequency computer-use command set served
// to authenticated SSH agents: one-shot subcommands, the NDJSON session
// protocol, and the sshdesk-agent-ssh allowlist wrapper.
package agent

import (
	"errors"
	"strings"
)

// maxCommandLength bounds one SSH command string and one NDJSON request line.
const maxCommandLength = 65_536

// maxTextLength bounds the characters injected by one type action.
const maxTextLength = 16_384

// errNoClosingQuotation mirrors Python shlex's "No closing quotation".
var errNoClosingQuotation = errors.New("No closing quotation")

// ShlexSplit splits s the way Python shlex.split does in POSIX mode: single
// quotes are fully literal, double quotes only let backslash escape '"' and
// '\', and an unquoted backslash escapes the next character (a backslash
// followed by a newline is a line continuation). Unlike Python, the POSIX
// rules are used on every platform.
func ShlexSplit(input string) ([]string, error) {
	runes := []rune(input)
	var tokens []string
	var token strings.Builder
	inToken := false
	const (
		stateDefault = iota
		stateSingle
		stateDouble
	)
	state := stateDefault
	for index := 0; index < len(runes); index++ {
		r := runes[index]
		switch state {
		case stateSingle:
			if r == '\'' {
				state = stateDefault
			} else {
				token.WriteRune(r)
			}
		case stateDouble:
			switch r {
			case '"':
				state = stateDefault
			case '\\':
				if index+1 < len(runes) && (runes[index+1] == '"' || runes[index+1] == '\\') {
					index++
					token.WriteRune(runes[index])
				} else {
					token.WriteRune(r)
				}
			default:
				token.WriteRune(r)
			}
		default:
			switch r {
			case '\'':
				state = stateSingle
				inToken = true
			case '"':
				state = stateDouble
				inToken = true
			case '\\':
				inToken = true
				if index+1 < len(runes) {
					index++
					if runes[index] != '\n' {
						token.WriteRune(runes[index])
					}
				} else {
					return nil, errNoClosingQuotation
				}
			case ' ', '\t', '\r', '\n':
				if inToken {
					tokens = append(tokens, token.String())
					token.Reset()
					inToken = false
				}
			default:
				token.WriteRune(r)
				inToken = true
			}
		}
	}
	if state != stateDefault {
		return nil, errNoClosingQuotation
	}
	if inToken {
		tokens = append(tokens, token.String())
	}
	return tokens, nil
}
