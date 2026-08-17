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

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
}

func TestResolveAtctPathPrefersConfiguredWrapper(t *testing.T) {
	siblingDir := socketDir(t)
	sibling := filepath.Join(siblingDir, "atct")
	writeExecutable(t, sibling)

	configuredDir := socketDir(t)
	configured := filepath.Join(configuredDir, "atct-wrapper")
	writeExecutable(t, configured)
	t.Setenv("ATCT_ATCT_BIN", configured)

	got := resolveAtctPath(filepath.Join(siblingDir, "atct-mcp"))
	if got != configured {
		t.Fatalf("resolveAtctPath = %q, want configured wrapper %q", got, configured)
	}
}

func TestResolveAtctPathPrefersSiblingBinary(t *testing.T) {
	dir := socketDir(t)
	sibling := filepath.Join(dir, "atct")
	writeExecutable(t, sibling)

	got := resolveAtctPath(filepath.Join(dir, "atct-mcp"))
	if got != sibling {
		t.Fatalf("resolveAtctPath = %q, want %q", got, sibling)
	}
}

func TestResolveAtctPathFallsBackWhenConfiguredWrapperIsMissing(t *testing.T) {
	dir := socketDir(t)
	sibling := filepath.Join(dir, "atct")
	writeExecutable(t, sibling)
	t.Setenv("ATCT_ATCT_BIN", filepath.Join(dir, "missing-atct"))

	got := resolveAtctPath(filepath.Join(dir, "atct-mcp"))
	if got != sibling {
		t.Fatalf("resolveAtctPath = %q, want sibling %q", got, sibling)
	}
}

func TestResolveAtctPathIgnoresNonExecutableConfiguredWrapper(t *testing.T) {
	dir := socketDir(t)
	nonExecutable := filepath.Join(dir, "atct-wrapper")
	if err := os.WriteFile(nonExecutable, []byte("not executable\n"), 0o644); err != nil {
		t.Fatalf("write non-executable wrapper: %v", err)
	}
	t.Setenv("ATCT_ATCT_BIN", nonExecutable)

	got := resolveAtctPath(filepath.Join(dir, "atct-mcp"))
	if got != "atct" {
		t.Fatalf("resolveAtctPath = %q, want %q for non-executable configured wrapper", got, "atct")
	}
}

func TestResolveAtctPathFallsBackToBareName(t *testing.T) {
	got := resolveAtctPath(filepath.Join(t.TempDir(), "atct-mcp"))
	if got != "atct" {
		t.Fatalf("resolveAtctPath = %q, want %q for PATH lookup", got, "atct")
	}
}
