package gnome

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rarnu/sshdesk-go/internal/input/mutter"
)

// busCall records one generic D-Bus call.
type busCall struct {
	destination string
	path        string
	iface       string
	method      string
	timeout     time.Duration
	args        []any
}

// fakeBus scripts the D-Bus session flow.
type fakeBus struct {
	state     DisplayState
	stateErr  error
	calls     []busCall
	replies   map[string][]any
	handler   func(node uint32)
	startHook func()
	closed    bool
}

func (b *fakeBus) currentState() (DisplayState, error) {
	if b.stateErr != nil {
		return DisplayState{}, b.stateErr
	}
	return b.state, nil
}

func (b *fakeBus) call(destination, path, iface, method string, timeout time.Duration, args ...any) ([]any, error) {
	b.calls = append(b.calls, busCall{destination, path, iface, method, timeout, args})
	if method == "Start" && b.startHook != nil {
		b.startHook()
	}
	if reply, ok := b.replies[method+"@"+path]; ok {
		return reply, nil
	}
	if reply, ok := b.replies[method]; ok {
		return reply, nil
	}
	return []any{}, nil
}

func (b *fakeBus) subscribe(path, iface, member string, handler func(node uint32)) (func(), error) {
	b.handler = handler
	return func() {}, nil
}

func (b *fakeBus) sessionCaller(path string) mutter.Caller {
	return func(method, signature string, values ...any) error { return nil }
}

func (b *fakeBus) close() { b.closed = true }

func openStreamFixture() (*fakeBus, *Capture) {
	fake := &fakeBus{
		state: twoMonitorState(2),
		replies: map[string][]any{
			"CreateSession@" + RemoteRoot:     {"/remote/session"},
			"Get@/remote/session":             {"session-1"},
			"CreateSession@" + ScreencastRoot: {"/screen/session"},
			"RecordArea@/screen/session":      {"/screen/stream"},
		},
	}
	fake.startHook = func() { go fake.handler(42) }
	capture := NewForBus(fake, "gst-launch-1.0")
	return fake, capture
}

func TestOpenStreamCallSequence(t *testing.T) {
	fake, capture := openStreamFixture()
	if err := capture.open(); err != nil {
		t.Fatalf("open() error = %v", err)
	}
	if capture.pipewireNode != 42 {
		t.Fatalf("pipewire node = %d, want 42", capture.pipewireNode)
	}
	if capture.desktopWidth != 4480 || capture.desktopHeight != 1600 {
		t.Fatalf("desktop size = %dx%d", capture.desktopWidth, capture.desktopHeight)
	}

	var methods []string
	for _, call := range fake.calls {
		methods = append(methods, call.destination+" "+call.path+" "+call.method)
	}
	want := []string{
		RemoteName + " " + RemoteRoot + " CreateSession",
		RemoteName + " /remote/session Get",
		ScreencastName + " " + ScreencastRoot + " CreateSession",
		ScreencastName + " /screen/session RecordArea",
		RemoteName + " /remote/session Start",
	}
	if !reflect.DeepEqual(methods, want) {
		t.Fatalf("call sequence = %v, want %v", methods, want)
	}

	get := fake.calls[1]
	if get.iface != PropertiesInterface || !reflect.DeepEqual(get.args, []any{RemoteSessionInterface, "SessionId"}) {
		t.Fatalf("SessionId Get = %+v", get)
	}
	create := fake.calls[2]
	if !reflect.DeepEqual(create.args, []any{map[string]any{"remote-desktop-session-id": "session-1"}}) {
		t.Fatalf("screen CreateSession args = %+v", create.args)
	}
	record := fake.calls[3]
	wantArgs := []any{int32(0), int32(0), int32(4480), int32(1600), map[string]any{"cursor-mode": uint32(0)}}
	if record.iface != ScreencastSessionInterface || !reflect.DeepEqual(record.args, wantArgs) {
		t.Fatalf("RecordArea = %s %+v", record.iface, record.args)
	}
}

func TestOpenStreamTimesOutWithoutNode(t *testing.T) {
	restore := streamTimeout
	streamTimeout = 50 * time.Millisecond
	t.Cleanup(func() { streamTimeout = restore })

	fake, capture := openStreamFixture()
	fake.startHook = nil
	err := capture.open()
	if err == nil || err.Error() != "GNOME did not publish its PipeWire desktop stream" {
		t.Fatalf("open() error = %v", err)
	}
}

func TestCloseStopsSessionsInOrder(t *testing.T) {
	fake := &fakeBus{}
	capture := NewForBus(fake, "gst-launch-1.0")
	capture.screenSessionPath = "/screen/session"
	capture.remoteSessionPath = "/remote/session"
	capture.streamPath = "/screen/stream"
	capture.pipewireNode = 42
	capture.hasCursor = true

	capture.Close()

	var stops []busCall
	for _, call := range fake.calls {
		stops = append(stops, call)
	}
	if len(stops) != 2 {
		t.Fatalf("Stop calls = %+v", stops)
	}
	if stops[0].destination != ScreencastName || stops[0].path != "/screen/session" || stops[0].method != "Stop" {
		t.Fatalf("first stop = %+v", stops[0])
	}
	if stops[1].destination != RemoteName || stops[1].path != "/remote/session" || stops[1].method != "Stop" {
		t.Fatalf("second stop = %+v", stops[1])
	}
	if stops[0].timeout != stopTimeout || stops[1].timeout != stopTimeout {
		t.Fatalf("stop timeouts = %v/%v", stops[0].timeout, stops[1].timeout)
	}
	if capture.pipewireNode != 0 || capture.hasCursor || capture.streamPath != "" {
		t.Fatal("Close() did not clear the stream state")
	}
	if !fake.closed {
		t.Fatal("Close() did not release the bus")
	}
	if _, _, ok := capture.CursorPosition(); ok {
		t.Fatal("cursor should be cleared")
	}
}

// fakeStream scripts stream frames and failures.
type fakeStream struct {
	frames []streamFrame
	err    error
	closed bool
	reads  int
}

func (s *fakeStream) Capture() (streamFrame, error) {
	s.reads++
	if s.err != nil {
		return streamFrame{}, s.err
	}
	if len(s.frames) == 0 {
		return streamFrame{}, errors.New("no frames scripted")
	}
	frame := s.frames[0]
	s.frames = s.frames[1:]
	return frame, nil
}

func (s *fakeStream) Close() { s.closed = true }

func TestCaptureRetriesOnceAfterStreamFailure(t *testing.T) {
	_, capture := openStreamFixture()
	if err := capture.open(); err != nil {
		t.Fatalf("open() error = %v", err)
	}
	first := &fakeStream{err: errors.New("stream ended")}
	second := &fakeStream{frames: []streamFrame{{RGB: make([]byte, 4480*1600*3), CapturedNs: 7, ContentDigest: []byte("12345678")}}}
	streams := []frameStream{first, second}
	created := 0
	capture.newStream = func(node uint32, width, height int) frameStream {
		created++
		return streams[created-1]
	}

	frame, err := capture.Capture()
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !first.closed {
		t.Fatal("the failed stream was not rebuilt")
	}
	if created != 2 {
		t.Fatalf("streams created = %d, want 2", created)
	}
	if frame.Width() != 4480 || frame.Height() != 1600 {
		t.Fatalf("desktop size = %dx%d", frame.Width(), frame.Height())
	}
}

func TestCaptureReportsFirstErrorAfterRetry(t *testing.T) {
	_, capture := openStreamFixture()
	if err := capture.open(); err != nil {
		t.Fatalf("open() error = %v", err)
	}
	capture.newStream = func(node uint32, width, height int) frameStream {
		return &fakeStream{err: errors.New("stream ended")}
	}
	_, err := capture.Capture()
	if err == nil || err.Error() != "GNOME PipeWire capture failed: stream ended" {
		t.Fatalf("Capture() error = %v", err)
	}
}

func TestCaptureClosedFails(t *testing.T) {
	_, capture := openStreamFixture()
	if err := capture.open(); err != nil {
		t.Fatalf("open() error = %v", err)
	}
	capture.Close()
	_, err := capture.Capture()
	if err == nil || err.Error() != "GNOME capture is closed" {
		t.Fatalf("Capture() error = %v", err)
	}
}

func TestSetTargetSizeClampsAndRestarts(t *testing.T) {
	_, capture := openStreamFixture()
	if err := capture.open(); err != nil {
		t.Fatalf("open() error = %v", err)
	}
	live := &fakeStream{}
	capture.stream = live
	if err := capture.SetTargetSize(16384, 100); err != nil {
		t.Fatalf("SetTargetSize() error = %v", err)
	}
	if capture.targetWidth != 4480 || capture.targetHeight != 100 {
		t.Fatalf("target = %dx%d, want 4480x100", capture.targetWidth, capture.targetHeight)
	}
	if !live.closed || capture.stream != nil {
		t.Fatal("SetTargetSize did not stop the live stream")
	}
	if err := capture.SetTargetSize(0, 100); err == nil {
		t.Fatal("SetTargetSize(0, 100) did not fail")
	}
}

func TestSetFrameRateValidation(t *testing.T) {
	capture := NewForBus(&fakeBus{}, "gst-launch-1.0")
	if err := capture.SetFrameRate(30); err != nil {
		t.Fatalf("SetFrameRate(30) error = %v", err)
	}
	if err := capture.SetFrameRate(500); err == nil || !strings.Contains(err.Error(), "between 0.5 and 120") {
		t.Fatalf("SetFrameRate(500) error = %v", err)
	}
}

func TestCreateInputBackendRequiresSession(t *testing.T) {
	capture := NewForBus(&fakeBus{}, "gst-launch-1.0")
	_, err := capture.CreateInputBackend()
	if err == nil || err.Error() != "the GNOME remote desktop session is unavailable" {
		t.Fatalf("CreateInputBackend() error = %v", err)
	}
}

func TestCreateInputBackendLinksSession(t *testing.T) {
	_, capture := openStreamFixture()
	if err := capture.open(); err != nil {
		t.Fatalf("open() error = %v", err)
	}
	backend, err := capture.CreateInputBackend()
	if err != nil {
		t.Fatalf("CreateInputBackend() error = %v", err)
	}
	backend.Move(10, 20)
	x, y, ok := capture.CursorPosition()
	if !ok || x != 10 || y != 20 {
		t.Fatalf("cursor = (%d, %d, %v), want (10, 20, true)", x, y, ok)
	}
}
