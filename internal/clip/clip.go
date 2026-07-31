// Package clip reads pasteable content from the system clipboard.
package clip

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"

	"golang.design/x/clipboard"
)

// MaxImageBytes bounds how much clipboard data the TUI keeps in memory for one
// attachment. Images are normalized to PNG by the clipboard package, so this is
// also the maximum size written into a session worktree.
const MaxImageBytes = 20 << 20

var ErrEmpty = errors.New("clipboard has no text or image")

// Image is one PNG image read from the clipboard.
type Image struct {
	PNG           []byte
	Width, Height int
}

// Content is the result of one paste. Image wins when a clipboard advertises
// both image and text representations; Text is the fallback for ordinary
// Ctrl+V behavior in the task composer.
type Content struct {
	Image *Image
	Text  string
}

// Read returns an image when the clipboard contains one, otherwise UTF-8 text.
func Read() (Content, error) {
	if err := clipboard.Init(); err != nil {
		return Content{}, fmt.Errorf("open clipboard: %w", err)
	}
	return decode(clipboard.Read(clipboard.FmtImage), clipboard.Read(clipboard.FmtText))
}

func decode(imageData, textData []byte) (Content, error) {
	if len(imageData) > 0 {
		if len(imageData) > MaxImageBytes {
			return Content{}, fmt.Errorf("clipboard image is %d MB; maximum is %d MB",
				(len(imageData)+(1<<20)-1)/(1<<20), MaxImageBytes>>20)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(imageData))
		if err != nil {
			return Content{}, fmt.Errorf("decode clipboard image: %w", err)
		}
		data := append([]byte(nil), imageData...)
		return Content{Image: &Image{PNG: data, Width: cfg.Width, Height: cfg.Height}}, nil
	}
	if len(textData) > 0 {
		return Content{Text: string(textData)}, nil
	}
	return Content{}, ErrEmpty
}
