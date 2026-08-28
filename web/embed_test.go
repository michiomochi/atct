package web

import (
	"io/fs"
	"os"
	"testing"
)

func TestDistEmbedMatchesSourceFiles(t *testing.T) {
	embedded, err := fs.Sub(Dist, "dist")
	if err != nil {
		t.Fatal(err)
	}

	sourceFiles := distFiles(t, os.DirFS("dist"))
	embeddedFiles := distFiles(t, embedded)

	for name := range sourceFiles {
		if _, ok := embeddedFiles[name]; !ok {
			t.Errorf("embedded dist is missing %q", name)
		}
	}
	for name := range embeddedFiles {
		if _, ok := sourceFiles[name]; !ok {
			t.Errorf("embedded dist contains unexpected file %q", name)
		}
	}
}

func distFiles(t *testing.T, filesystem fs.FS) map[string]struct{} {
	t.Helper()

	files := make(map[string]struct{})
	err := fs.WalkDir(filesystem, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files[name] = struct{}{}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
