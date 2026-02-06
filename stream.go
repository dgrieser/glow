package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/glow/v2/utils"
	"github.com/mattn/go-runewidth"
)

const (
	streamRenderInterval = 200 * time.Millisecond
	streamMinColWidth    = 12
)

type streamChunk struct {
	data []byte
	err  error
	eof  bool
}

type streamTableLayouts struct {
	widthsByTable map[int][]int
}

func newStreamTableLayouts() *streamTableLayouts {
	return &streamTableLayouts{widthsByTable: map[int][]int{}}
}

func (s *streamTableLayouts) layout(tableIdx int, headers []string) []int {
	if widths, ok := s.widthsByTable[tableIdx]; ok {
		return widths
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = max(streamMinColWidth, runewidth.StringWidth(strings.TrimSpace(h))+2)
	}
	s.widthsByTable[tableIdx] = widths
	return widths
}

func executeStreamCLI(src *source, w io.Writer) error {
	chunks := make(chan streamChunk, 16)
	go readStream(src.reader, chunks)

	ticker := time.NewTicker(streamRenderInterval)
	defer ticker.Stop()

	layouts := newStreamTableLayouts()
	var input bytes.Buffer
	lastRendered := ""
	dirty := false

	emit := func(final bool) error {
		rendered, err := renderStreamSnapshot(input.String(), layouts, final)
		if err != nil {
			return err
		}
		rendered = normalizeStreamOutput(rendered)
		if rendered == lastRendered {
			return nil
		}

		delta := streamDelta(lastRendered, rendered)
		if delta == "" {
			return nil
		}
		if _, err := io.WriteString(w, delta); err != nil {
			return fmt.Errorf("unable to write stream output: %w", err)
		}
		lastRendered = rendered
		return nil
	}

	for {
		select {
		case chunk := <-chunks:
			if chunk.err != nil {
				return fmt.Errorf("unable to read from reader: %w", chunk.err)
			}
			if len(chunk.data) > 0 {
				if _, err := input.Write(chunk.data); err != nil {
					return fmt.Errorf("unable to buffer stream input: %w", err)
				}
				dirty = true
			}
			if chunk.eof {
				if err := emit(true); err != nil {
					return err
				}
				if lastRendered != "" {
					if _, err := io.WriteString(w, "\n\n"); err != nil {
						return fmt.Errorf("unable to write stream output: %w", err)
					}
				}
				return nil
			}
		case <-ticker.C:
			if !dirty {
				continue
			}
			if err := emit(false); err != nil {
				return err
			}
			dirty = false
		}
	}
}

func readStream(r io.Reader, out chan<- streamChunk) {
	defer close(out)
	reader := bufio.NewReader(r)
	buf := make([]byte, 4096)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			out <- streamChunk{data: chunk}
		}
		if err != nil {
			if err == io.EOF {
				out <- streamChunk{eof: true}
				return
			}
			out <- streamChunk{err: err}
			return
		}
	}
}

func renderStreamSnapshot(content string, layouts *streamTableLayouts, final bool) (string, error) {
	if !final && !strings.Contains(content, "\n") {
		return "", nil
	}

	prepared := preprocessStreamMarkdown(content, layouts, final)
	options := []glamour.TermRendererOption{
		glamour.WithColorProfile(lipgloss.ColorProfile()),
		utils.GlamourStyle(style, false),
		glamour.WithWordWrap(int(width)), //nolint:gosec
		glamour.WithPreservedNewLines(),
	}
	r, err := glamour.NewTermRenderer(options...)
	if err != nil {
		return "", fmt.Errorf("unable to create renderer: %w", err)
	}

	out, err := r.Render(prepared)
	if err != nil {
		return "", fmt.Errorf("unable to render markdown: %w", err)
	}
	return out, nil
}

func normalizeStreamOutput(s string) string {
	if s == "" {
		return s
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func streamDelta(prev, next string) string {
	if prev == "" {
		return next
	}
	if strings.HasPrefix(next, prev) {
		return next[len(prev):]
	}

	limit := min(len(prev), len(next))
	i := 0
	for i < limit && prev[i] == next[i] {
		i++
	}

	// Keep append-only chunks aligned to full lines.
	if j := strings.LastIndex(next[:i], "\n"); j >= 0 {
		i = j + 1
	} else {
		i = 0
	}
	return next[i:]
}

func preprocessStreamMarkdown(content string, layouts *streamTableLayouts, final bool) string {
	processable := content
	if !final {
		lastNewline := strings.LastIndex(processable, "\n")
		if lastNewline < 0 {
			return ""
		}
		processable = processable[:lastNewline+1]
	}

	lines := strings.Split(processable, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if !final && len(lines) > 0 {
		// Emit only up to the most recent blank-line boundary when possible.
		// This keeps block-level markdown (lists, paragraphs, headings) from
		// retroactively changing already-emitted output in stream mode.
		commitCount := 0
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.TrimSpace(lines[i]) == "" {
				commitCount = i + 1
				break
			}
		}

		if commitCount == 0 {
			// Fallback for continuous logs without blank lines: keep one line
			// buffered to reduce churn from multi-line constructs.
			commitCount = len(lines) - 1
			if commitCount > 0 && isSetextUnderlineLine(lines[commitCount-1]) {
				commitCount--
			}
		}
		if commitCount < 0 {
			commitCount = 0
		}
		lines = lines[:commitCount]
	}
	var b strings.Builder
	tableIdx := 0

	for i := 0; i < len(lines); {
		if i+1 < len(lines) && isTableHeaderLine(lines[i]) && isTableSeparatorLine(lines[i+1]) {
			headers := parseTableCells(lines[i])
			if len(headers) == 0 {
				b.WriteString(lines[i])
				b.WriteRune('\n')
				i++
				continue
			}

			widths := layouts.layout(tableIdx, headers)
			tableIdx++

			rows := make([][]string, 0)
			j := i + 2
			for j < len(lines) {
				line := lines[j]
				if !isTableRowLine(line) {
					break
				}
				rows = append(rows, parseTableCells(line))
				j++
			}

			committedRows := len(rows)

			if committedRows > 0 {
				b.WriteString("```text\n")
				b.WriteString(formatFixedWidthTable(headers, widths, rows[:committedRows]))
				b.WriteString("```\n")
			}

			i = j
			continue
		}

		b.WriteString(lines[i])
		b.WriteRune('\n')
		i++
	}

	out := b.String()
	if hasUnclosedCodeFence(out) {
		out += "\n```\n"
	}

	return out
}

func formatFixedWidthTable(headers []string, widths []int, rows [][]string) string {
	colCount := len(widths)
	if colCount == 0 {
		return ""
	}

	headers = normalizeCells(headers, colCount)
	var b strings.Builder

	b.WriteString(formatTableRow(headers, widths))
	b.WriteString(formatTableSeparator(widths))

	for _, row := range rows {
		cells := normalizeCells(row, colCount)
		b.WriteString(formatTableRow(cells, widths))
	}

	return b.String()
}

func formatTableSeparator(widths []int) string {
	var b strings.Builder
	b.WriteRune('|')
	for _, width := range widths {
		b.WriteString(strings.Repeat("-", max(1, width)))
		b.WriteRune('|')
	}
	b.WriteRune('\n')
	return b.String()
}

func formatTableRow(cells []string, widths []int) string {
	wrapped := make([][]string, len(widths))
	height := 1

	for i := range widths {
		contentWidth := max(1, widths[i]-2)
		wrapped[i] = wrapCell(cells[i], contentWidth)
		height = max(height, len(wrapped[i]))
	}

	var b strings.Builder
	for lineIdx := 0; lineIdx < height; lineIdx++ {
		b.WriteRune('|')
		for colIdx, width := range widths {
			contentWidth := max(1, width-2)
			segment := ""
			if lineIdx < len(wrapped[colIdx]) {
				segment = wrapped[colIdx][lineIdx]
			}

			padding := max(0, contentWidth-runewidth.StringWidth(segment))
			b.WriteRune(' ')
			b.WriteString(segment)
			b.WriteString(strings.Repeat(" ", padding))
			b.WriteRune(' ')
			b.WriteRune('|')
		}
		b.WriteRune('\n')
	}

	return b.String()
}

func wrapCell(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}

	cell := strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if cell == "" {
		return []string{""}
	}

	words := strings.Fields(cell)
	if len(words) == 0 {
		return []string{""}
	}

	lines := make([]string, 0, 1)
	cur := ""
	for _, word := range words {
		if runewidth.StringWidth(word) > width {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
			}
			parts := breakWord(word, width)
			lines = append(lines, parts...)
			continue
		}

		candidate := word
		if cur != "" {
			candidate = cur + " " + word
		}
		if runewidth.StringWidth(candidate) <= width {
			cur = candidate
			continue
		}
		lines = append(lines, cur)
		cur = word
	}

	if cur != "" {
		lines = append(lines, cur)
	}

	return lines
}

func breakWord(word string, width int) []string {
	if width <= 0 || word == "" {
		return []string{word}
	}

	parts := []string{}
	remaining := word
	for runewidth.StringWidth(remaining) > width {
		part := runewidth.Truncate(remaining, width, "")
		parts = append(parts, part)
		remaining = strings.TrimPrefix(remaining, part)
	}
	if remaining != "" {
		parts = append(parts, remaining)
	}
	if len(parts) == 0 {
		parts = append(parts, "")
	}
	return parts
}

func normalizeCells(cells []string, cols int) []string {
	out := make([]string, cols)
	for i := 0; i < cols; i++ {
		if i < len(cells) {
			out[i] = strings.TrimSpace(cells[i])
		}
	}
	return out
}

func isTableHeaderLine(s string) bool {
	trimmed := strings.TrimSpace(s)
	return strings.Contains(trimmed, "|") && trimmed != ""
}

func isTableSeparatorLine(s string) bool {
	cells := parseTableCells(s)
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		v := strings.TrimSpace(strings.Trim(c, ":"))
		if len(v) < 3 {
			return false
		}
		for _, r := range v {
			if r != '-' {
				return false
			}
		}
	}
	return true
}

func isTableRowLine(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, "|")
}

func isSetextUnderlineLine(s string) bool {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < 3 {
		return false
	}
	ch := trimmed[0]
	if ch != '=' && ch != '-' {
		return false
	}
	for i := 1; i < len(trimmed); i++ {
		if trimmed[i] != ch {
			return false
		}
	}
	return true
}

func parseTableCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "|") {
		trimmed = strings.TrimPrefix(trimmed, "|")
	}
	if strings.HasSuffix(trimmed, "|") {
		trimmed = strings.TrimSuffix(trimmed, "|")
	}

	parts := make([]string, 0)
	var cur strings.Builder
	escaped := false
	for _, r := range trimmed {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|':
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	parts = append(parts, strings.TrimSpace(cur.String()))

	return parts
}

func hasUnclosedCodeFence(s string) bool {
	open := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			open = !open
		}
	}
	return open
}
