package main

import "testing"

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantListen string
		wantErr    bool
	}{
		{
			name:       "daemon parses listen flag after subcommand",
			args:       []string{"daemon", "-listen", "127.0.0.1:18787"},
			wantListen: "127.0.0.1:18787",
		},
		{
			name:       "daemon uses loopback default",
			args:       []string{"daemon"},
			wantListen: defaultListenAddr,
		},
		{
			name:    "missing subcommand is rejected",
			args:    []string{},
			wantErr: true,
		},
		{
			name:    "unknown subcommand is rejected",
			args:    []string{"serve"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseArgs(%q) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.listenAddr != tt.wantListen {
				t.Fatalf("parseArgs(%q) listenAddr = %q, want %q", tt.args, got.listenAddr, tt.wantListen)
			}
		})
	}
}

func TestParseArgsAcceptsEnsure(t *testing.T) {
	cfg, err := parseArgs([]string{"ensure"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "ensure" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "ensure")
	}
}

func TestParseArgsAcceptsStop(t *testing.T) {
	cfg, err := parseArgs([]string{"stop"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "stop" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "stop")
	}
}

func TestParseArgsAcceptsListenOnEnsure(t *testing.T) {
	cfg, err := parseArgs([]string{"ensure", "-listen", "127.0.0.1:19999"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.listenAddr != "127.0.0.1:19999" {
		t.Fatalf("listenAddr = %q, want %q", cfg.listenAddr, "127.0.0.1:19999")
	}
}

func TestParseArgsAcceptsProjectAdd(t *testing.T) {
	cfg, err := parseArgs([]string{"project", "add", "myproj"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "project" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "project")
	}
	if cfg.projectAction != "add" {
		t.Fatalf("projectAction = %q, want %q", cfg.projectAction, "add")
	}
	if cfg.projectName != "myproj" {
		t.Fatalf("projectName = %q, want %q", cfg.projectName, "myproj")
	}
}

func TestParseArgsAcceptsProjectAddWithoutName(t *testing.T) {
	cfg, err := parseArgs([]string{"project", "add"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.projectName != "" {
		t.Fatalf("projectName = %q, want empty", cfg.projectName)
	}
}

func TestParseArgsAcceptsProjectList(t *testing.T) {
	cfg, err := parseArgs([]string{"project", "list"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "project" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "project")
	}
	if cfg.projectAction != "list" {
		t.Fatalf("projectAction = %q, want %q", cfg.projectAction, "list")
	}
}

func TestParseArgsRejectsUnknownProjectAction(t *testing.T) {
	if _, err := parseArgs([]string{"project", "remove"}); err == nil {
		t.Fatal("parseArgs(project remove) returned nil error")
	}
}

func TestParseArgsRejectsProjectWithoutAction(t *testing.T) {
	if _, err := parseArgs([]string{"project"}); err == nil {
		t.Fatal("parseArgs(project) returned nil error")
	}
}
