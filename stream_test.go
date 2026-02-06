package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func TestStreamSnapshotPrefixForNewlineInput(t *testing.T) {
	layouts := newStreamTableLayouts()
	style = "dark"
	width = 80

	first, err := renderStreamSnapshot("a\nb\n", layouts, false)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}
	second, err := renderStreamSnapshot("a\nb\nc\n", layouts, false)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	first = normalizeStreamOutput(first)
	second = normalizeStreamOutput(second)

	if !strings.HasPrefix(second, first) {
		t.Fatalf("expected second snapshot to extend first\nfirst:\n%q\nsecond:\n%q", first, second)
	}
}

func TestStreamSnapshotPrefixForSetextHeading(t *testing.T) {
	layouts := newStreamTableLayouts()
	style = "dark"
	width = 80

	first, err := renderStreamSnapshot("Title\n", layouts, false)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}
	second, err := renderStreamSnapshot("Title\n=====\n", layouts, false)
	if err != nil {
		t.Fatalf("unexpected render error: %v", err)
	}

	first = normalizeStreamOutput(first)
	second = normalizeStreamOutput(second)

	if !strings.HasPrefix(second, first) {
		t.Fatalf("expected second snapshot to extend first\nfirst:\n%q\nsecond:\n%q", first, second)
	}
}

func TestPreprocessCommitsOnlyToBlankLineBoundary(t *testing.T) {
	layouts := newStreamTableLayouts()
	in := "a\nb\n\nc\n"
	out := preprocessStreamMarkdown(in, layouts, false)

	if strings.Contains(out, "c") {
		t.Fatalf("expected trailing block to remain buffered, output:\n%s", out)
	}
	if !strings.Contains(out, "a") || !strings.Contains(out, "b") {
		t.Fatalf("expected committed block to be present, output:\n%s", out)
	}
}

func TestStreamDeltaUsesCommonPrefix(t *testing.T) {
	prev := "a\nb\nc\n"
	next := "a\nb\nX\nc\n"
	got := streamDelta(prev, next)

	if got != "X\nc\n" {
		t.Fatalf("unexpected delta: %q", got)
	}
}

func FuzzStreamDeltaAppendOnly(f *testing.F) {
	f.Add("a\nb\n", "a\nb\nc\n")
	f.Add("Title\n", "Title\n=====\n")
	f.Add("", "hello\n")
	f.Add("abc", "xyz")

	f.Fuzz(func(t *testing.T, prev, next string) {
		delta := streamDelta(prev, next)

		if strings.HasPrefix(next, prev) && delta != next[len(prev):] {
			t.Fatalf("prefix case must emit exact suffix: prev=%q next=%q delta=%q", prev, next, delta)
		}

		if delta != "" && !strings.HasSuffix(next, delta) && !strings.Contains(next, delta) {
			t.Fatalf("delta must come from next snapshot: next=%q delta=%q", next, delta)
		}
	})
}

func TestStreamPTYNoReplayFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY integration test in short mode")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skipf("script not available: %v", err)
	}

	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "glow")
	fixturePath := filepath.Join(tmpDir, "fixture.md")
	typescriptPath := filepath.Join(tmpDir, "stream.typescript")

	fixture := strings.TrimSpace(`
🌐 Searching the web...
🌐 Searched: OpenAI Codex GPT-5 model documentation

thinking
**Planning primary source search**
🌐 Searching the web...
🌐 Searched: Anthropic Claude Opus 4.6 announcement

thinking
**Planning multi-source verification**
🌐 Searching the web...
🌐 Searched: https://openai.com/index/introducing-gpt-5-3-codex/

thinking
**Drafting balanced model comparison**
codex
As of **February 6, 2026**, here’s the practical comparison:

- **Positioning**
  - **GPT-5.3-Codex**: coding-first, agentic computer work, optimized for Codex workflows.
  - **Claude Opus 4.6**: broad “smartest” model positioning across coding + knowledge work (research, finance, docs/spreadsheets).

- **Availability**
  - **GPT-5.3-Codex**: available in paid ChatGPT plans and Codex surfaces; API access is stated as coming soon.
  - **Opus 4.6**: available now in consumer app, API, and major cloud platforms.
`)
	if err := os.WriteFile(fixturePath, []byte(fixture+"\n"), 0o600); err != nil {
		t.Fatalf("unable to write fixture: %v", err)
	}

	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(tmpDir, ".gocache"))
	buildOut, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, string(buildOut))
	}

	streamCmd := fmt.Sprintf(
		"cat %s | while IFS= read -r line; do echo \"$line\"; sleep 0.005; done | %s --stream",
		strconv.Quote(fixturePath),
		strconv.Quote(binPath),
	)
	cmd := exec.Command("script", "-q", "-c", streamCmd, typescriptPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "failed to create pseudo-terminal") {
			t.Skipf("pty unavailable in this environment: %s", strings.TrimSpace(string(out)))
		}
		t.Fatalf("pty stream run failed: %v\n%s", err, string(out))
	}

	typescript, err := os.ReadFile(typescriptPath)
	if err != nil {
		t.Fatalf("unable to read typescript output: %v", err)
	}
	got := string(typescript)

	sentinel := "Searched: OpenAI Codex GPT-5 model documentation"
	if c := strings.Count(got, sentinel); c != 1 {
		t.Fatalf("expected sentinel %q exactly once, got %d", sentinel, c)
	}
	heading := "Drafting balanced model comparison"
	if c := strings.Count(got, heading); c != 1 {
		t.Fatalf("expected heading %q exactly once, got %d", heading, c)
	}
}
