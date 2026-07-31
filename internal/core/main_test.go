package core

import (
	"os"
	"testing"
)

// The state directory defaults to ~/.dma. A test that writes there overwrites
// the developer's own board, so the whole package is pointed at a temp dir
// before any test runs.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "dma-core-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("DMA_HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
