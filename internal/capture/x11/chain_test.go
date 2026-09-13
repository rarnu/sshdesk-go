package x11

import (
	"errors"
	"image"
	"testing"
)

var errTierBoom = errors.New("tier exploded")

// fakeTier is a scripted capture tier for chain tests.
type fakeTier struct {
	err    error
	frames int
	closed bool
}

func (t *fakeTier) capture() (Frame, error) {
	if t.err != nil {
		return Frame{}, t.err
	}
	t.frames++
	return Frame{Image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, nil
}

func (t *fakeTier) close() { t.closed = true }

func TestChainAutoDisablesFailedFFmpegTier(t *testing.T) {
	ffmpegAttempts := 0
	shared := &fakeTier{}
	fallbackCalls := 0
	c := &chain{
		backend: "auto",
		newFFmpeg: func() (tier, error) {
			ffmpegAttempts++
			return nil, errTierBoom
		},
		newShared: func() (tier, error) { return shared, nil },
		fallback: func() (Frame, error) {
			fallbackCalls++
			return Frame{}, nil
		},
	}

	if _, err := c.capture(); err != nil {
		t.Fatalf("capture() error = %v", err)
	}
	if ffmpegAttempts != 1 || shared.frames != 1 || fallbackCalls != 0 {
		t.Fatalf("first capture: ffmpeg attempts=%d shared frames=%d fallbacks=%d", ffmpegAttempts, shared.frames, fallbackCalls)
	}
	if !c.ffmpegDisabled {
		t.Fatal("capture() did not disable the failed ffmpeg tier")
	}

	if _, err := c.capture(); err != nil {
		t.Fatalf("capture() error = %v", err)
	}
	if ffmpegAttempts != 1 {
		t.Fatalf("second capture retried the disabled ffmpeg tier (%d attempts)", ffmpegAttempts)
	}
	if shared.frames != 2 {
		t.Fatalf("second capture did not reuse the shared tier (%d frames)", shared.frames)
	}
}

func TestChainExplicitFFmpegRaises(t *testing.T) {
	live := &fakeTier{err: errTierBoom}
	sharedCalls := 0
	fallbackCalls := 0
	c := &chain{
		backend:   "ffmpeg",
		newFFmpeg: func() (tier, error) { return live, nil },
		newShared: func() (tier, error) {
			sharedCalls++
			return &fakeTier{}, nil
		},
		fallback: func() (Frame, error) {
			fallbackCalls++
			return Frame{}, nil
		},
	}

	if _, err := c.capture(); !errors.Is(err, errTierBoom) {
		t.Fatalf("capture() error = %v, want the tier error", err)
	}
	if sharedCalls != 0 || fallbackCalls != 0 {
		t.Fatalf("explicit ffmpeg fell through: shared=%d fallback=%d", sharedCalls, fallbackCalls)
	}
	if c.ffmpegDisabled {
		t.Fatal("explicit ffmpeg must not be marked disabled")
	}
	if !live.closed || c.ffmpeg != nil {
		t.Fatal("the failed ffmpeg tier was not closed")
	}
}

func TestChainSharedFallsBackToGetImage(t *testing.T) {
	sharedAttempts := 0
	fallbackCalls := 0
	c := &chain{
		backend:        "auto",
		ffmpegDisabled: true,
		newFFmpeg:      func() (tier, error) { return &fakeTier{}, nil },
		newShared: func() (tier, error) {
			sharedAttempts++
			return nil, errTierBoom
		},
		fallback: func() (Frame, error) {
			fallbackCalls++
			return Frame{Image: image.NewRGBA(image.Rect(0, 0, 1, 1))}, nil
		},
	}

	if _, err := c.capture(); err != nil {
		t.Fatalf("capture() error = %v", err)
	}
	if sharedAttempts != 1 || fallbackCalls != 1 {
		t.Fatalf("first capture: shared attempts=%d fallbacks=%d", sharedAttempts, fallbackCalls)
	}
	if !c.sharedDisabled {
		t.Fatal("capture() did not disable the failed shared tier")
	}

	if _, err := c.capture(); err != nil {
		t.Fatalf("capture() error = %v", err)
	}
	if sharedAttempts != 1 || fallbackCalls != 2 {
		t.Fatalf("second capture: shared attempts=%d fallbacks=%d", sharedAttempts, fallbackCalls)
	}
}

func TestChainExplicitXSHMRaises(t *testing.T) {
	fallbackCalls := 0
	c := &chain{
		backend:        "xshm",
		ffmpegDisabled: true,
		newFFmpeg:      func() (tier, error) { return &fakeTier{}, nil },
		newShared:      func() (tier, error) { return nil, errTierBoom },
		fallback: func() (Frame, error) {
			fallbackCalls++
			return Frame{}, nil
		},
	}

	if _, err := c.capture(); !errors.Is(err, errTierBoom) {
		t.Fatalf("capture() error = %v, want the tier error", err)
	}
	if fallbackCalls != 0 {
		t.Fatal("explicit xshm fell through to the fallback")
	}
	if c.sharedDisabled {
		t.Fatal("explicit xshm must not be marked disabled")
	}
}

func TestChainCloseClosesLiveTiers(t *testing.T) {
	ffmpegLive := &fakeTier{}
	sharedLive := &fakeTier{}
	c := &chain{
		backend:   "auto",
		newFFmpeg: func() (tier, error) { return ffmpegLive, nil },
		newShared: func() (tier, error) { return sharedLive, nil },
		fallback:  func() (Frame, error) { return Frame{}, nil },
	}

	if _, err := c.capture(); err != nil {
		t.Fatalf("capture() error = %v", err)
	}
	c.close()
	if !ffmpegLive.closed || c.ffmpeg != nil {
		t.Fatal("close() did not release the ffmpeg tier")
	}
	if sharedLive.closed || c.shared != nil {
		t.Fatal("close() must not touch the never-started shared tier")
	}
}
