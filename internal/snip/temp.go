package snip

import (
	"os"
	"path/filepath"
)

type tempNamedFile struct {
	path string
	dir  string
}

func tempFileWithName(name string, content []byte) (tempNamedFile, error) {
	dir, err := os.MkdirTemp("", "snip-create-")
	if err != nil {
		return tempNamedFile{}, err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return tempNamedFile{}, err
	}
	return tempNamedFile{path: path, dir: dir}, nil
}

func (t tempNamedFile) cleanup() { _ = os.RemoveAll(t.dir) }
