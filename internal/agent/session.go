package agent

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// jsonInt converts a decoded JSON value the way Python int() does.
func jsonInt(value any) (int, error) {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("cannot convert float %v to integer", value)
		}
		return int(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("invalid literal for int() with base 10: %q", v)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("int() argument must be a string or a number")
	}
}

// jsonFloat converts a decoded JSON value the way Python float() does.
func jsonFloat(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("could not convert string to float: %q", v)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("float() argument must be a string or a real number")
	}
}

// jsonString mirrors Python str() for the JSON values agents send.
func jsonString(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// truthy mirrors Python truth testing for decoded JSON values.
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}

func orDefault(request map[string]any, name string, fallback any) any {
	if value, ok := request[name]; ok {
		return value
	}
	return fallback
}

// requiredInt fetches a mandatory numeric field; the error echoes Python's
// KeyError representation for a missing key.
func requiredInt(request map[string]any, name string) (int, error) {
	value, ok := request[name]
	if !ok {
		return 0, fmt.Errorf("'%s'", name)
	}
	return jsonInt(value)
}

func requiredCoordinate(request map[string]any, name string) (int, error) {
	value, ok := request[name]
	if !ok {
		return 0, fmt.Errorf("'%s'", name)
	}
	parsed, err := jsonInt(value)
	if err != nil {
		return 0, err
	}
	if parsed < -16384 || parsed > 65535 {
		return 0, fmt.Errorf("coordinate is outside the supported range")
	}
	return parsed, nil
}

// sessionAction executes one decoded session request and returns the extra
// response fields, mirroring the Python _session_action.
func sessionAction(controller *Controller, request map[string]any) (map[string]any, error) {
	action := ""
	if raw, ok := request["action"]; ok {
		action = jsonString(raw)
	}
	switch action {
	case "info":
		return controller.Info()
	case "observe", "screenshot":
		maximum := 0
		if raw, ok := request["max_width"]; ok {
			parsed, err := jsonInt(raw)
			if err != nil {
				return nil, err
			}
			maximum = max(0, min(parsed, 4096))
		}
		image, width, height, err := controller.Screenshot(maximum)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"width":        width,
			"height":       height,
			"format":       "png",
			"image_base64": base64.StdEncoding.EncodeToString(image),
		}, nil
	case "move":
		x, err := requiredCoordinate(request, "x")
		if err != nil {
			return nil, err
		}
		y, err := requiredCoordinate(request, "y")
		if err != nil {
			return nil, err
		}
		return map[string]any{}, controller.Move(x, y)
	case "click":
		button := jsonString(orDefault(request, "button", "left"))
		if _, ok := buttons[button]; !ok {
			return nil, fmt.Errorf("button must be left, middle, or right")
		}
		x, err := requiredCoordinate(request, "x")
		if err != nil {
			return nil, err
		}
		y, err := requiredCoordinate(request, "y")
		if err != nil {
			return nil, err
		}
		count := 1
		if raw, ok := request["count"]; ok {
			parsed, err := jsonInt(raw)
			if err != nil {
				return nil, err
			}
			count = max(1, min(parsed, 20))
		}
		return map[string]any{}, controller.Click(x, y, button, count)
	case "scroll":
		amount, err := requiredInt(request, "amount")
		if err != nil {
			return nil, err
		}
		x, err := requiredCoordinate(request, "x")
		if err != nil {
			return nil, err
		}
		y, err := requiredCoordinate(request, "y")
		if err != nil {
			return nil, err
		}
		return map[string]any{}, controller.Scroll(amount, x, y)
	case "type":
		text := jsonString(orDefault(request, "text", ""))
		interval, err := jsonFloat(orDefault(request, "interval_ms", float64(0)))
		if err != nil {
			return nil, err
		}
		return map[string]any{}, controller.TypeText(text, interval)
	case "key":
		raw, ok := request["key"]
		if !ok {
			return nil, fmt.Errorf("'key'")
		}
		mods := modifiers(func(name string) bool { return truthy(request[name]) })
		return map[string]any{}, controller.Key(jsonString(raw), mods)
	case "wait":
		seconds, err := jsonFloat(orDefault(request, "seconds", float64(0)))
		if err != nil {
			return nil, err
		}
		time.Sleep(time.Duration(max(0.0, min(seconds, 10.0)) * float64(time.Second)))
		return map[string]any{}, nil
	case "quit":
		return map[string]any{"quit": true}, nil
	}
	return nil, fmt.Errorf("unknown action: %s", action)
}

// marshalCompact renders one response as Python json.dumps does with
// separators=(",", ":") and ensure_ascii=True: no HTML escaping, non-ASCII
// content escaped as \uXXXX.
func marshalCompact(response map[string]any) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		return []byte(`{"ok":false,"error":"could not encode the response"}`)
	}
	raw := bytes.TrimRight(buffer.Bytes(), "\n")
	var out bytes.Buffer
	out.Grow(len(raw) + 8)
	for _, r := range string(raw) {
		if r < 128 {
			out.WriteRune(r)
			continue
		}
		if r > 0xFFFF {
			r -= 0x10000
			fmt.Fprintf(&out, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
			continue
		}
		fmt.Fprintf(&out, `\u%04x`, r)
	}
	return out.Bytes()
}

// RunSession serves newline-delimited JSON requests until EOF or a quit
// action, mirroring the Python run_agent_session. Errors never stop the
// session.
func RunSession(controller *Controller, stdin io.Reader, stdout io.Writer) int {
	reader := bufio.NewReader(stdin)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var response map[string]any
			if len(line) > maxCommandLength {
				response = map[string]any{"ok": false, "error": "request is too large"}
			} else {
				response = serveOneLine(controller, line)
			}
			stdout.Write(marshalCompact(response))
			stdout.Write([]byte("\n"))
			flush(stdout)
			if truthy(response["quit"]) {
				return 0
			}
		}
		if readErr != nil {
			return 0
		}
	}
}

func flush(writer io.Writer) {
	if flusher, ok := writer.(interface{ Flush() error }); ok {
		_ = flusher.Flush()
	}
}

func serveOneLine(controller *Controller, line []byte) map[string]any {
	var decoded any
	if err := json.Unmarshal(line, &decoded); err != nil {
		return map[string]any{"id": nil, "ok": false, "error": err.Error()}
	}
	request, ok := decoded.(map[string]any)
	if !ok {
		return map[string]any{"id": nil, "ok": false, "error": "request must be a JSON object"}
	}
	requestID := request["id"]
	result, err := sessionAction(controller, request)
	if err != nil {
		return map[string]any{"id": requestID, "ok": false, "error": err.Error()}
	}
	response := map[string]any{"id": requestID, "ok": true}
	for key, value := range result {
		response[key] = value
	}
	return response
}
