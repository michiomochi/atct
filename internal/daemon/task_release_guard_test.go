package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDaemonDoesNotCallHumanTaskRelease(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find daemon package directory")
	}

	entries, err := os.ReadDir(filepath.Dir(filename))
	if err != nil {
		t.Fatalf("read daemon package directory: %v", err)
	}

	checkedFiles := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		checkedFiles++
		path := filepath.Join(filepath.Dir(filename), name)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %v: %v", name, err)
		}
		if strings.Contains(string(contents), "ReleaseTaskForHuman(") {
			t.Errorf("%v calls human task release path", name)
		}
	}
	if checkedFiles == 0 {
		t.Fatal("checked no daemon source files")
	}
}
