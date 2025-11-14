package filesystem

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath resolves "~" to the user home directory and returns an absolute path.
func ExpandPath(path string) (string, error) {
	if path == "" {
		return path, nil
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return filepath.Abs(path)
}
