package main

import "testing"

func TestParseArgsTokenUsage(t *testing.T) {
	cfg, err := parseArgs([]string{"token-usage", "--since", "2026-10-03", "--role", "executor", "--goal", "7", "--json", "--prefix", "p"})
	if err != nil {
		t.Fatal(err)
	}
	o := cfg.tokenUsage
	if o == nil || o.role != "executor" || o.goal == nil || *o.goal != 7 || !o.json || o.prefix != "p" || o.since.Format("2006-01-02") != "2026-10-03" {
		t.Fatalf("options = %+v", o)
	}
	for _, bad := range [][]string{{"token-usage", "--role", "boss"}, {"token-usage", "--since", "yesterday"}, {"token-usage", "extra"}} {
		if _, err := parseArgs(bad); err == nil {
			t.Errorf("parseArgs(%v) accepted", bad)
		}
	}
}
