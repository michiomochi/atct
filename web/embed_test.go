package web

import (
	"io/fs"
	"os"
	"path"
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

	for _, locale := range []string{"en", "ja"} {
		if _, ok := sourceFiles[path.Join(locale, "index.html")]; !ok {
			t.Errorf("%s locale tree is missing index.html", locale)
		}
		entries, err := fs.ReadDir(os.DirFS("dist"), path.Join(locale, "_astro"))
		if err != nil {
			t.Errorf("read %s asset tree: %v", locale, err)
			continue
		}
		if len(entries) == 0 {
			t.Errorf("%s asset tree is empty", locale)
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
