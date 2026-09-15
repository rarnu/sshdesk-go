// Package terminal parses terminal escape sequences into normalized input
// events. It is an incremental state machine handling UTF-8 keys, SGR and
// legacy X10 mouse reporting, and cursor-position reports.
package terminal

import (
	"errors"
	"regexp"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rarnu/sshdesk-go/internal/input"
	"github.com/rarnu/sshdesk-go/internal/render"
)

var (
	mouseRe        = regexp.MustCompile(`^\x1b\[<(\d{1,5});(\d{1,5});(\d{1,5})([Mm])`)
	csiKeyRe       = regexp.MustCompile(`^\x1b\[(?:(\d+)(?:;(\d+))?)?([A-DFHZ])`)
	csiTildeRe     = regexp.MustCompile(`^\x1b\[(\d+)(?:;(\d+))?~`)
	cursorReportRe = regexp.MustCompile(`^\x1b\[(\d{1,4});(\d{1,4})R`)
)

var legacyMousePrefix = []byte("\x1b[M")

var csiKeys = map[byte]input.KeyCode{
	'A': input.KeyUp,
	'B': input.KeyDown,
	'C': input.KeyRight,
	'D': input.KeyLeft,
	'H': input.KeyHome,
	'F': input.KeyEnd,
	'Z': input.KeyTab,
}

var tildeKeys = map[int]input.KeyCode{
	1:  input.KeyHome,
	2:  input.KeyInsert,
	3:  input.KeyDelete,
	4:  input.KeyEnd,
	5:  input.KeyPageUp,
	6:  input.KeyPageDown,
	15: input.KeyF5,
	17: input.KeyF6,
	18: input.KeyF7,
	19: input.KeyF8,
	20: input.KeyF9,
	21: input.KeyF10,
	23: input.KeyF11,
	24: input.KeyF12,
}

type keySequence struct {
	sequence string
	code     input.KeyCode
}

var sequences = []keySequence{
	{"\x1b[15~", input.KeyF5},
	{"\x1b[17~", input.KeyF6},
	{"\x1b[18~", input.KeyF7},
	{"\x1b[19~", input.KeyF8},
	{"\x1b[20~", input.KeyF9},
	{"\x1b[21~", input.KeyF10},
	{"\x1b[23~", input.KeyF11},
	{"\x1b[24~", input.KeyF12},
	{"\x1b[2~", input.KeyInsert},
	{"\x1b[3~", input.KeyDelete},
	{"\x1b[5~", input.KeyPageUp},
	{"\x1b[6~", input.KeyPageDown},
	{"\x1b[A", input.KeyUp},
	{"\x1b[B", input.KeyDown},
	{"\x1b[C", input.KeyRight},
	{"\x1b[D", input.KeyLeft},
	{"\x1b[H", input.KeyHome},
	{"\x1b[F", input.KeyEnd},
	{"\x1bOH", input.KeyHome},
	{"\x1bOF", input.KeyEnd},
	{"\x1bOP", input.KeyF1},
	{"\x1bOQ", input.KeyF2},
	{"\x1bOR", input.KeyF3},
	{"\x1bOS", input.KeyF4},
}

// EscapeDelay disambiguates a bare ESC key press from escape sequences.
const EscapeDelay = 0.035

const maxBuffer = 8192

// ErrOversizedSequence rejects terminal input sequences beyond the buffer cap.
var ErrOversizedSequence = errors.New("terminal input sequence exceeds 8192 bytes")

// TranslateCoordinates maps zero-based terminal cells into remote desktop
// coordinates, or returns ok=false outside the letterboxed viewport.
func TranslateCoordinates(column, row int, viewport render.Viewport) (int, int, bool) {
	if !(viewport.X <= column && column < viewport.X+viewport.Width &&
		viewport.Y <= row && row < viewport.Y+viewport.Height) {
		return 0, 0, false
	}
	localX := column - viewport.X
	localY := row - viewport.Y
	remoteX := min(viewport.DesktopWidth-1,
		int((float64(localX)+0.5)*float64(viewport.DesktopWidth)/float64(viewport.Width)))
	remoteY := min(viewport.DesktopHeight-1,
		int((float64(localY)+0.5)*float64(viewport.DesktopHeight)/float64(viewport.Height)))
	return remoteX, remoteY, true
}

// CoalesceMouseMoves keeps the newest motion in each uninterrupted run of
// mouse moves.
func CoalesceMouseMoves(events []input.Event) []input.Event {
	compacted := make([]input.Event, 0, len(events))
	for _, event := range events {
		move, isMove := event.(input.MouseMoveEvent)
		if isMove && len(compacted) > 0 {
			if _, previousIsMove := compacted[len(compacted)-1].(input.MouseMoveEvent); previousIsMove {
				compacted[len(compacted)-1] = move
				continue
			}
		}
		compacted = append(compacted, event)
	}
	return compacted
}

// Parser is an incremental parser for UTF-8 keys and terminal mouse reports.
type Parser struct {
	buffer        []byte
	detachPending bool
	escapeSince   float64
	hasEscape     bool
	legacyButton  int
}

func tap(code input.KeyCode, modifiers input.Modifiers, character rune) input.KeyEvent {
	return input.KeyEvent{Action: input.KeyTap, Modifiers: modifiers, Code: code, Unicode: character}
}

func utf8Length(first byte) int {
	switch {
	case first < 0x80:
		return 1
	case first >= 0xC2 && first <= 0xDF:
		return 2
	case first >= 0xE0 && first <= 0xEF:
		return 3
	case first >= 0xF0 && first <= 0xF4:
		return 4
	default:
		return 1
	}
}

func hasPrefix(buffer []byte, prefix string) bool {
	if len(buffer) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if buffer[i] != prefix[i] {
			return false
		}
	}
	return true
}

func prefixOfBytes(buffer []byte, prefix []byte) bool {
	if len(buffer) > len(prefix) {
		return false
	}
	for i := 0; i < len(buffer); i++ {
		if buffer[i] != prefix[i] {
			return false
		}
	}
	return true
}

func anySequenceHasPrefix(buffer []byte) bool {
	for _, candidate := range sequences {
		if prefixOfBytes(buffer, []byte(candidate.sequence)) {
			return true
		}
	}
	return false
}

// Feed consumes terminal bytes and returns the completed input events.
func (p *Parser) Feed(data []byte, now float64) ([]input.Event, error) {
	p.buffer = append(p.buffer, data...)
	if len(p.buffer) > maxBuffer {
		p.buffer = p.buffer[:0]
		return nil, ErrOversizedSequence
	}
	var events []input.Event
	for len(p.buffer) > 0 {
		first := p.buffer[0]
		if p.detachPending {
			p.detachPending = false
			if first == 0x1D {
				p.buffer = p.buffer[1:]
				events = append(events, input.ControlEvent{Kind: input.ControlExit})
				continue
			}
			events = append(events, tap(input.KeyCharacter, input.ModCtrl, ']'))
		}
		switch {
		case first == 0x1D: // Ctrl+], twice detaches locally.
			p.buffer = p.buffer[1:]
			p.detachPending = true
		case first == 0x13: // Ctrl+Shift+S is encoded as Ctrl+S by terminals.
			p.buffer = p.buffer[1:]
			events = append(events, input.ControlEvent{Kind: input.ControlToggleStats})
		case first == 10 || first == 13:
			p.buffer = p.buffer[1:]
			events = append(events, tap(input.KeyEnter, input.ModNone, 0))
		case first == 8 || first == 127:
			p.buffer = p.buffer[1:]
			events = append(events, tap(input.KeyBackspace, input.ModNone, 0))
		case first == 9:
			p.buffer = p.buffer[1:]
			events = append(events, tap(input.KeyTab, input.ModNone, 0))
		case first >= 1 && first <= 26:
			p.buffer = p.buffer[1:]
			events = append(events, tap(input.KeyCharacter, input.ModCtrl, rune('a'+first-1)))
		case first == 0x1B:
			if match := cursorReportRe.FindSubmatch(p.buffer); match != nil {
				row, _ := strconv.Atoi(string(match[1]))
				column, _ := strconv.Atoi(string(match[2]))
				row = max(1, row) - 1
				column = max(1, column) - 1
				p.buffer = p.buffer[len(match[0]):]
				events = append(events, input.TerminalReportEvent{Column: column, Row: row})
				p.hasEscape = false
				continue
			}
			if match := mouseRe.FindSubmatch(p.buffer); match != nil {
				buttonCode, _ := strconv.Atoi(string(match[1]))
				column, _ := strconv.Atoi(string(match[2]))
				row, _ := strconv.Atoi(string(match[3]))
				suffix := match[4][0]
				p.buffer = p.buffer[len(match[0]):]
				column = max(0, column-1)
				row = max(0, row-1)
				switch {
				case buttonCode&64 != 0:
					amount := 1
					if buttonCode&1 != 0 {
						amount = -1
					}
					events = append(events, input.MouseScrollEvent{Amount: amount, Column: column, Row: row})
				case buttonCode&32 != 0:
					events = append(events, input.MouseMoveEvent{Column: column, Row: row})
				default:
					button := (buttonCode & 3) + 1
					if button <= 3 {
						events = append(events, input.MouseButtonEvent{Button: button, Pressed: suffix == 'M', Column: column, Row: row})
					}
				}
				p.hasEscape = false
				continue
			}
			if hasPrefix(p.buffer, string(legacyMousePrefix)) {
				if len(p.buffer) < 6 {
					return events, nil
				}
				buttonCode := int(p.buffer[3]) - 32
				column := max(0, int(p.buffer[4])-33)
				row := max(0, int(p.buffer[5])-33)
				p.buffer = p.buffer[6:]
				baseButton := buttonCode & 3
				switch {
				case buttonCode&64 != 0:
					amount := 1
					if buttonCode&1 != 0 {
						amount = -1
					}
					events = append(events, input.MouseScrollEvent{Amount: amount, Column: column, Row: row})
				case buttonCode&32 != 0:
					events = append(events, input.MouseMoveEvent{Column: column, Row: row})
				case baseButton == 3:
					button := p.legacyButton
					if button == 0 {
						button = 1
					}
					events = append(events, input.MouseButtonEvent{Button: button, Pressed: false, Column: column, Row: row})
					p.legacyButton = 0
				default:
					button := baseButton + 1
					p.legacyButton = button
					events = append(events, input.MouseButtonEvent{Button: button, Pressed: true, Column: column, Row: row})
				}
				p.hasEscape = false
				continue
			}
			if len(p.buffer) > 1 && prefixOfBytes(p.buffer, legacyMousePrefix) {
				return events, nil
			}
			if match := csiKeyRe.FindSubmatch(p.buffer); match != nil {
				modifiers := xtermModifiers(match[2])
				final := match[3][0]
				if final == 'Z' {
					modifiers |= input.ModShift
				}
				p.buffer = p.buffer[len(match[0]):]
				events = append(events, tap(csiKeys[final], modifiers, 0))
				p.hasEscape = false
				continue
			}
			if match := csiTildeRe.FindSubmatch(p.buffer); match != nil {
				number, _ := strconv.Atoi(string(match[1]))
				code, known := tildeKeys[number]
				if known {
					modifiers := xtermModifiers(match[2])
					p.buffer = p.buffer[len(match[0]):]
					events = append(events, tap(code, modifiers, 0))
					p.hasEscape = false
					continue
				}
			}
			matched := false
			for _, candidate := range sequences {
				if hasPrefix(p.buffer, candidate.sequence) {
					p.buffer = p.buffer[len(candidate.sequence):]
					events = append(events, tap(candidate.code, input.ModNone, 0))
					p.hasEscape = false
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			sgrPrefix := "\x1b[<"
			if anySequenceHasPrefix(p.buffer) ||
				prefixOfBytes(p.buffer, []byte(sgrPrefix)) ||
				hasPrefix(p.buffer, sgrPrefix) {
				if p.hasEscape && now-p.escapeSince >= EscapeDelay {
					p.buffer = p.buffer[1:]
					events = append(events, tap(input.KeyEscape, input.ModNone, 0))
					p.hasEscape = false
					continue
				}
				if !p.hasEscape {
					p.escapeSince = now
					p.hasEscape = true
				}
				return events, nil
			}
			if hasPrefix(p.buffer, "\x1b[") && !hasFinalByte(p.buffer[2:]) {
				if !p.hasEscape {
					p.escapeSince = now
					p.hasEscape = true
				}
				if now-p.escapeSince < EscapeDelay {
					return events, nil
				}
			}
			if len(p.buffer) >= 2 && p.buffer[1] != '[' && p.buffer[1] != 'O' {
				p.buffer = p.buffer[1:]
				length := utf8Length(p.buffer[0])
				if len(p.buffer) < length {
					p.buffer = append([]byte{0x1B}, p.buffer...)
					return events, nil
				}
				encoded := p.buffer[:length]
				character, size := utf8.DecodeRune(encoded)
				if character == utf8.RuneError && size <= 1 {
					character = '�'
					length = 1
				}
				p.buffer = p.buffer[length:]
				events = append(events, tap(input.KeyCharacter, input.ModAlt, character))
				p.hasEscape = false
				continue
			}
			if !p.hasEscape {
				p.escapeSince = now
				p.hasEscape = true
			}
			if now-p.escapeSince < EscapeDelay {
				return events, nil
			}
			p.buffer = p.buffer[1:]
			events = append(events, tap(input.KeyEscape, input.ModNone, 0))
			p.hasEscape = false
		default:
			length := utf8Length(first)
			if len(p.buffer) < length {
				return events, nil
			}
			encoded := p.buffer[:length]
			character, size := utf8.DecodeRune(encoded)
			if character == utf8.RuneError && size <= 1 {
				character = '�'
				length = 1
			}
			p.buffer = p.buffer[length:]
			modifiers := input.ModNone
			if unicode.IsLetter(character) && unicode.IsUpper(character) {
				modifiers = input.ModShift
			}
			events = append(events, tap(input.KeyCharacter, modifiers, character))
		}
	}
	return events, nil
}

// FeedNow feeds data using the current monotonic clock.
func (p *Parser) FeedNow(data []byte) ([]input.Event, error) {
	return p.Feed(data, monotonic())
}

// Flush re-evaluates the buffered state, typically to let a pending bare ESC
// age past the disambiguation delay.
func (p *Parser) Flush(now float64) ([]input.Event, error) {
	return p.Feed(nil, now)
}

// FlushNow flushes using the current monotonic clock.
func (p *Parser) FlushNow() ([]input.Event, error) {
	return p.Feed(nil, monotonic())
}

func monotonic() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

func hasFinalByte(data []byte) bool {
	for _, value := range data {
		if value >= 0x40 && value <= 0x7E {
			return true
		}
	}
	return false
}

func xtermModifiers(parameter []byte) input.Modifiers {
	if parameter == nil {
		return input.ModNone
	}
	value, err := strconv.Atoi(string(parameter))
	if err != nil {
		return input.ModNone
	}
	value = max(0, value-1)
	modifiers := input.ModNone
	if value&1 != 0 {
		modifiers |= input.ModShift
	}
	if value&2 != 0 {
		modifiers |= input.ModAlt
	}
	if value&4 != 0 {
		modifiers |= input.ModCtrl
	}
	return modifiers
}
