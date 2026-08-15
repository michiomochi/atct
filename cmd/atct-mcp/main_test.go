package main

import (
	"os"
	"path/filepath"
	"testing"
)

func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestResolveAtctPathPrefersSiblingBinary(t *testing.T) {
	dir := socketDir(t)
	sibling := filepath.Join(dir, "atct")
	if err := os.WriteFile(sibling, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write sibling: %v", err)
	}

	got := resolveAtctPath(filepath.Join(dir, "atct-mcp"))
	if got != sibling {
		t.Fatalf("resolveAtctPath = %q, want %q", got, sibling)
	}
}

func TestResolveAtctPathFallsBackToBareName(t *testing.T) {
	got := resolveAtctPath(filepath.Join(t.TempDir(), "atct-mcp"))
	if got != "atct" {
		t.Fatalf("resolveAtctPath = %q, want %q for PATH lookup", got, "atct")
	}
}
