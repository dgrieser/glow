package main

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestStreamTableLayoutFrozen(t *testing.T) {
	layouts := newStreamTableLayouts()
	w1 := layouts.layout(0, []string{"id", "note"})
	w2 := layouts.layout(0, []string{"identifier", "an extremely long column header"})

	if len(w1) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(w1))
	}
	if w1[0] != 12 || w1[1] != 12 {
		t.Fatalf("expected min fixed widths of 12, got %v", w1)
	}
	if w2[0] != w1[0] || w2[1] != w1[1] {
		t.Fatalf("expected frozen widths %v, got %v", w1, w2)
	}
}

func TestPreprocessBuffersLastStreamingRow(t *testing.T) {
	layouts := newStreamTableLayouts()

	first := "| id | note |\n| --- | --- |\n| 1 | hello world |\n"
	out := preprocessStreamMarkdown(first, layouts, false)
	if strings.Contains(out, "hello world") {
		t.Fatalf("expected last row to stay buffered, output:\n%s", out)
	}

	second := first + "| 2 | second row |\n"
	out = preprocessStreamMarkdown(second, layouts, false)
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Fatalf("expected first row to be emitted after second row arrives, output:\n%s", out)
	}
	if strings.Contains(out, "second row") {
		t.Fatalf("expected newest row to stay buffered, output:\n%s", out)
	}
}

func TestTableFormattingWrapsWithinFixedWidth(t *testing.T) {
	table := formatFixedWidthTable(
		[]string{"id", "note"},
		[]int{12, 12},
		[][]string{{"1", "supercalifragilisticexpialidocious"}},
	)

	for _, line := range strings.Split(table, "\n") {
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "----") {
			continue
		}
		parts := strings.Split(line, "|")
		for _, cell := range parts {
			cell = strings.TrimSpace(cell)
			if runewidth.StringWidth(cell) > 10 {
				t.Fatalf("expected wrapped cell width <= 10, got %q (%d)", cell, runewidth.StringWidth(cell))
			}
		}
	}
}
