package gnome

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/rarnu/sshdesk-go/internal/input/mutter"
)

// D-Bus typed views of Mutter's GetCurrentState reply.
type dbusMonitorSpec struct {
	Connector string
	Vendor    string
	Product   string
	Serial    string
}

type dbusMonitorMode struct {
	ID              string
	Width           int32
	Height          int32
	RefreshRate     float64
	PreferredScale  float64
	SupportedScales []float64
	Properties      map[string]dbus.Variant
}

type dbusMonitor struct {
	Spec       dbusMonitorSpec
	Modes      []dbusMonitorMode
	Properties map[string]dbus.Variant
}

type dbusLogicalMonitor struct {
	X          int32
	Y          int32
	Scale      float64
	Transform  uint32
	Primary    bool
	Monitors   []dbusMonitorSpec
	Properties map[string]dbus.Variant
}

// dbusBus is the production session-bus implementation.
type dbusBus struct {
	conn *dbus.Conn
}

func connectSessionBus() (*dbusBus, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	return &dbusBus{conn: conn}, nil
}

func (b *dbusBus) close() { b.conn.Close() }

// convertArgs turns the portable argument values into godbus values;
// map[string]any becomes the a{sv} dictionary Mutter expects.
func convertArgs(args []any) []any {
	converted := make([]any, len(args))
	for index, arg := range args {
		if dictionary, ok := arg.(map[string]any); ok {
			variants := make(map[string]dbus.Variant, len(dictionary))
			for key, value := range dictionary {
				variants[key] = dbus.MakeVariant(value)
			}
			converted[index] = variants
			continue
		}
		converted[index] = arg
	}
	return converted
}

// unwrapBody replaces top-level variants and object paths with plain Go
// values so the portable layer never sees godbus types.
func unwrapBody(body []any) []any {
	unwrapped := make([]any, len(body))
	for index, value := range body {
		switch typed := value.(type) {
		case dbus.Variant:
			unwrapped[index] = typed.Value()
		case dbus.ObjectPath:
			unwrapped[index] = string(typed)
		default:
			unwrapped[index] = value
		}
	}
	return unwrapped
}

func (b *dbusBus) call(destination, path, iface, method string, timeout time.Duration, args ...any) ([]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	object := b.conn.Object(destination, dbus.ObjectPath(path))
	result := object.CallWithContext(ctx, iface+"."+method, 0, convertArgs(args)...)
	if result.Err != nil {
		return nil, fmt.Errorf("GNOME %s failed: %v", method, result.Err)
	}
	return unwrapBody(result.Body), nil
}

// variantBool reads a boolean a{sv} entry.
func variantBool(properties map[string]dbus.Variant, key string) bool {
	if variant, ok := properties[key]; ok {
		if value, ok := variant.Value().(bool); ok {
			return value
		}
	}
	return false
}

func (b *dbusBus) currentState() (DisplayState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	object := b.conn.Object(DisplayName, DisplayRoot)
	result := object.CallWithContext(ctx, DisplayInterface+".GetCurrentState", 0)
	if result.Err != nil {
		return DisplayState{}, fmt.Errorf("GNOME %s failed: %v", "GetCurrentState", result.Err)
	}
	var serial uint32
	var monitors []dbusMonitor
	var logical []dbusLogicalMonitor
	var properties map[string]dbus.Variant
	if err := result.Store(&serial, &monitors, &logical, &properties); err != nil {
		return DisplayState{}, errors.New("GNOME returned an invalid display configuration")
	}
	_ = serial

	state := DisplayState{LayoutMode: 2}
	if variant, ok := properties["layout-mode"]; ok {
		if value, ok := variant.Value().(uint32); ok {
			state.LayoutMode = int(value)
		}
	}
	for _, monitor := range monitors {
		parsed := Monitor{Spec: MonitorSpec(monitor.Spec)}
		for _, mode := range monitor.Modes {
			parsed.Modes = append(parsed.Modes, MonitorMode{
				ID:        mode.ID,
				Width:     int(mode.Width),
				Height:    int(mode.Height),
				IsCurrent: variantBool(mode.Properties, "is-current"),
			})
		}
		state.Monitors = append(state.Monitors, parsed)
	}
	for _, entry := range logical {
		parsed := LogicalMonitor{
			X:         int(entry.X),
			Y:         int(entry.Y),
			Scale:     entry.Scale,
			Transform: int(entry.Transform),
			Primary:   entry.Primary,
		}
		for _, spec := range entry.Monitors {
			parsed.Monitors = append(parsed.Monitors, MonitorSpec(spec))
		}
		state.Logical = append(state.Logical, parsed)
	}
	return state, nil
}

// subscribe registers one PipeWireStreamAdded signal match and forwards the
// node id until the returned unsubscribe is called.
func (b *dbusBus) subscribe(path, iface, member string, handler func(node uint32)) (func(), error) {
	options := []dbus.MatchOption{
		dbus.WithMatchSender(ScreencastName),
		dbus.WithMatchObjectPath(dbus.ObjectPath(path)),
		dbus.WithMatchInterface(iface),
		dbus.WithMatchMember(member),
	}
	if err := b.conn.AddMatchSignal(options...); err != nil {
		return nil, err
	}
	signals := make(chan *dbus.Signal, 8)
	b.conn.Signal(signals)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case signal := <-signals:
				if signal == nil {
					return
				}
				if node, ok := signal.Body[0].(uint32); ok && node > 0 {
					handler(node)
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		b.conn.RemoveSignal(signals)
		_ = b.conn.RemoveMatchSignal(options...)
	}, nil
}

// sessionCaller exposes the remote desktop session to the Mutter input
// backend.
func (b *dbusBus) sessionCaller(path string) mutter.Caller {
	return mutter.NewDBusCaller(b.conn, dbus.ObjectPath(path))
}
