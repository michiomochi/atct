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
