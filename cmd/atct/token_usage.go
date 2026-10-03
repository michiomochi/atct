package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/tokenusage"
)

type tokenUsageOptions struct {
	since  time.Time
	role   string
	goal   *int
	json   bool
	root   string
	prefix string
}

// parseTokenUsageArgs reads the flags of `atct token-usage`; it never needs the daemon.
func parseTokenUsageArgs(args []string) (*tokenUsageOptions, error) {
	flags := flag.NewFlagSet("token-usage", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	o := &tokenUsageOptions{}
	since := flags.String("since", "2026-10-03", "only transcripts modified on or after this date (YYYY-MM-DD)")
	flags.StringVar(&o.role, "role", "", "only this role: commander, subcommander, executor or unknown")
	goal := flags.Int("goal", 0, "only this goal's worktree sessions")
	flags.BoolVar(&o.json, "json", false, "print JSON instead of tables")
	home, _ := os.UserHomeDir()
	flags.StringVar(&o.root, "root", filepath.Join(home, ".claude", "projects"), "directory of Claude Code transcripts")
	flags.StringVar(&o.prefix, "prefix", "", "main checkout directory name under root (default: derived from git)")
	if err := flags.Parse(args); err != nil {
		return nil, errInvalidArgs
	}
	if len(flags.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n", flags.Args()[0])
		return nil, errInvalidArgs
	}
	t, err := time.ParseInLocation("2006-01-02", *since, time.Local)
	if err != nil {
		fmt.Fprintf(os.Stderr, "token-usage: --since must be YYYY-MM-DD: %v\n", err)
		return nil, errInvalidArgs
	}
	o.since = t
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "goal" {
			o.goal = goal
		}
	})
	if o.role != "" {
		ok := false
		for _, r := range tokenusage.Roles {
			ok = ok || r == o.role
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "token-usage: --role must be one of %s\n", strings.Join(tokenusage.Roles, ", "))
			return nil, errInvalidArgs
		}
	}
	return o, nil
}

var nonDirChars = regexp.MustCompile(`[^A-Za-z0-9-]`)

// projectDirName is the transcript directory name of the main checkout:
// the parent of git's common dir, with every other character turned into "-".
func projectDirName() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("derive --prefix from git (pass --prefix outside a repository): %w", err)
	}
	return nonDirChars.ReplaceAllString(filepath.Dir(strings.TrimSpace(string(out))), "-"), nil
}

func runTokenUsage(o *tokenUsageOptions) error {
	prefix := o.prefix
	if prefix == "" {
		var err error
		if prefix, err = projectDirName(); err != nil {
			return err
		}
	}
	all, err := tokenusage.Collect(o.root, prefix, o.since)
	if err != nil {
		return err
	}
	var picked []*tokenusage.Session
	for _, s := range all {
		if (o.role == "" || s.Role == o.role) && (o.goal == nil || (s.Goal != nil && *s.Goal == *o.goal)) {
			picked = append(picked, s)
		}
	}
	rep := tokenusage.Build(picked, all, tokenusage.MergeTimes("."))
	if !o.json {
		tokenusage.Show(os.Stdout, rep)
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	return enc.Encode(rep)
}
