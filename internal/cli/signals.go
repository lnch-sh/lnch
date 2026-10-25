package cli

import (
	"os"
	"path/filepath"
)

func (g *globals) projectRoot() (string, error) {
	if g.Root == "" {
		return os.Getwd()
	}
	return filepath.Abs(g.Root)
}
