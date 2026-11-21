package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNoRawSQLCallsOutsideMigrations(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find store package directory")
	}

	entries, err := os.ReadDir(filepath.Dir(filename))
	if err != nil {
		t.Fatalf("read store package directory: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") || name == "migrations.go" {
			continue
		}

		path := filepath.Join(filepath.Dir(filename), name)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for lineNumber, line := range strings.Split(string(contents), "\n") {
			for _, method := range []string{"Exec", "Query", "QueryRow", "ExecContext", "QueryContext", "QueryRowContext"} {
				if strings.Contains(line, "."+method+"(") {
					t.Errorf("%s:%d: raw SQL method %s; use sqlc", name, lineNumber+1, method)
				}
			}
		}
	}
}
