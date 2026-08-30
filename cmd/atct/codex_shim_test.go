package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCodexShimInstallsExecutableAndProfileBlock(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(profile, []byte("# existing profile\n"), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	if err := writeCodexShim(home, profile, "/opt/atct"); err != nil {
		t.Fatalf("writeCodexShim: %v", err)
	}

	shimPath := filepath.Join(home, ".atct", "bin", "codex")
	shim, err := os.ReadFile(shimPath)
	if err != nil {
		t.Fatalf("read installed shim: %v", err)
	}
	shimText := string(shim)
	if !strings.Contains(shimText, codexShimMarker) {
		t.Fatalf("shim = %q, want marker %q", shimText, codexShimMarker)
	}
	if !strings.Contains(shimText, "exec '/opt/atct' codex shim run -- \"$@\"") {
		t.Fatalf("shim = %q, want opaque launcher invocation", shimText)
	}
	info, err := os.Stat(shimPath)
	if err != nil {
		t.Fatalf("stat installed shim: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("shim mode = %o, want executable", info.Mode().Perm())
	}

	profileText, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if !strings.Contains(string(profileText), codexShimProfileBeginMarker) || !strings.Contains(string(profileText), codexShimProfileEndMarker) {
		t.Fatalf("profile = %q, want marked PATH block", profileText)
	}
}

func TestWriteCodexShimIsIdempotent(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(profile, []byte("# existing profile\n"), 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := writeCodexShim(home, profile, "/opt/atct"); err != nil {
			t.Fatalf("writeCodexShim iteration %d: %v", i, err)
		}
	}

	profileText, err := os.ReadFile(profile)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if got := strings.Count(string(profileText), codexShimProfileBeginMarker); got != 1 {
		t.Fatalf("profile begin marker count = %d, want 1; profile = %q", got, profileText)
	}
	if got := strings.Count(string(profileText), codexShimProfileEndMarker); got != 1 {
		t.Fatalf("profile end marker count = %d, want 1; profile = %q", got, profileText)
	}
}

func TestWriteCodexShimPreservesUnmarkedCollision(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".zshrc")
	profileOriginal := []byte("# keep this profile\n")
	if err := os.WriteFile(profile, profileOriginal, 0o600); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	shimPath := filepath.Join(home, ".atct", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(shimPath), 0o700); err != nil {
		t.Fatalf("create shim directory: %v", err)
	}
	original := []byte("#!/bin/sh\necho user codex\n")
	if err := os.WriteFile(shimPath, original, 0o700); err != nil {
		t.Fatalf("write existing codex: %v", err)
	}

	err := writeCodexShim(home, profile, "/opt/atct")
	if err == nil {
		t.Fatal("writeCodexShim returned nil for unmarked existing codex")
	}
	got, readErr := os.ReadFile(shimPath)
	if readErr != nil {
		t.Fatalf("read existing codex: %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("existing codex = %q, want unchanged %q", got, original)
	}
	profileGot, readErr := os.ReadFile(profile)
	if readErr != nil {
		t.Fatalf("read existing profile: %v", readErr)
	}
	if string(profileGot) != string(profileOriginal) {
		t.Fatalf("existing profile = %q, want unchanged %q", profileGot, profileOriginal)
	}
}

func TestRunCodexShimInstallWithoutSupportedProfilePrintsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")

	var (
		code int
		err  error
	)
	output := captureStderr(t, func() {
		code, err = runCodexShimInstall(cliConfig{codexShimAction: "install"}, "/opt/atct")
	})
	if err != nil {
		t.Fatalf("runCodexShimInstall: %v", err)
	}
	if code != 0 {
		t.Fatalf("runCodexShimInstall code = %d, want 0", code)
	}
	if !strings.Contains(output, codexShimPathLine(filepath.Join(home, ".atct", "bin"))) {
		t.Fatalf("output = %q, want PATH line", output)
	}
	for _, profile := range []string{filepath.Join(home, ".zshrc"), filepath.Join(home, ".bashrc")} {
		if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("profile %s stat error = %v, want absent", profile, err)
		}
	}
}

func TestDefaultCodexShimProfileSelectsSupportedShells(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		shell string
		want  string
	}{
		{shell: "/bin/zsh", want: filepath.Join(home, ".zshrc")},
		{shell: "/usr/bin/bash", want: filepath.Join(home, ".bashrc")},
		{shell: "/bin/fish", want: ""},
	}
	for _, tt := range tests {
		t.Run(filepath.Base(tt.shell), func(t *testing.T) {
			if got := defaultCodexShimProfile(tt.shell, home); got != tt.want {
				t.Fatalf("defaultCodexShimProfile(%q, %q) = %q, want %q", tt.shell, home, got, tt.want)
			}
		})
	}
}
