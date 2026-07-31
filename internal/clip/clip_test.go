package clip

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDecodePrefersImageAndCopiesBytes(t *testing.T) {
	raw := testPNG(t, 3, 2)
	got, err := decode(raw, []byte("text fallback"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Image == nil || got.Text != "" {
		t.Fatalf("decode = %+v, want image only", got)
	}
	if got.Image.Width != 3 || got.Image.Height != 2 {
		t.Errorf("dimensions = %dx%d, want 3x2", got.Image.Width, got.Image.Height)
	}
	raw[0] = 0
	if got.Image.PNG[0] == 0 {
		t.Error("clipboard buffer was retained instead of copied")
	}
}

func TestDecodeFallsBackToText(t *testing.T) {
	got, err := decode(nil, []byte("paste me"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != nil || got.Text != "paste me" {
		t.Fatalf("decode = %+v, want text", got)
	}
}

func TestDecodeRejectsInvalidAndOversizedImages(t *testing.T) {
	if _, err := decode([]byte("not a png"), nil); err == nil ||
		!strings.Contains(err.Error(), "decode clipboard image") {
		t.Fatalf("invalid PNG error = %v", err)
	}
	if _, err := decode(make([]byte, MaxImageBytes+1), nil); err == nil ||
		!strings.Contains(err.Error(), "maximum") {
		t.Fatalf("oversized PNG error = %v", err)
	}
}

func TestDecodeReportsEmptyClipboard(t *testing.T) {
	if _, err := decode(nil, nil); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty clipboard error = %v, want ErrEmpty", err)
	}
}
