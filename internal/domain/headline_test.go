package domain

import "testing"

func TestHeadlineReturnsContentWithoutNewline(t *testing.T) {
	const content = "single line goal"

	if got := Headline(content); got != content {
		t.Fatalf("Headline(%q) = %q, want %q", content, got, content)
	}
}

func TestHeadlineReturnsFirstLine(t *testing.T) {
	const content = "first line\n\nlonger explanation"

	if got := Headline(content); got != "first line" {
		t.Fatalf("Headline(%q) = %q, want %q", content, got, "first line")
	}
}

func TestHeadlineTrimsWhitespace(t *testing.T) {
	const content = "  first line  \n\n  explanation  "

	if got := Headline(content); got != "first line" {
		t.Fatalf("Headline(%q) = %q, want %q", content, got, "first line")
	}
}

func TestHeadlineReturnsEmptyForEmptyContent(t *testing.T) {
	if got := Headline(""); got != "" {
		t.Fatalf("Headline(%q) = %q, want empty string", "", got)
	}
}

func TestHeadlineStripsHeadingMarker(t *testing.T) {
	cases := []struct {
		content string
		want    string
	}{
		{"# T\n本文", "T"},
		{"  # T  \n本文", "T"},
		{"#   T", "T"},
	}
	for _, c := range cases {
		if got := Headline(c.content); got != c.want {
			t.Fatalf("Headline(%q) = %q, want %q", c.content, got, c.want)
		}
	}
}

func TestStripHeadingMarker(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"# T", "T"},
		{"###### T", "T"},
		{"####### T", "####### T"},
		{"#T", "#T"},
		{"#", ""},
		{"#   T", "T"},
		{"  # T", "T"},
		{"# T #", "T #"},
		{"plain", "plain"},
		{"", ""},
	}
	for _, c := range cases {
		if got := StripHeadingMarker(c.line); got != c.want {
			t.Fatalf("StripHeadingMarker(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}
