package splitter

import (
	"strings"
	"testing"
)

func TestSplitPlainText(t *testing.T) {
	text := "line1\nline2\nline3\nline4\nline5\n"
	pieces := SplitContent([]byte(text), "test.md", 102400, 20)

	// Rejoin should produce original
	joined := strings.Join(pieces, "")
	if joined != text {
		t.Errorf("rejoin mismatch: got %q, want %q", joined, text)
	}
}

func TestSplitBinary(t *testing.T) {
	data := make([]byte, 300000) // 300KB
	for i := range data {
		data[i] = byte(i % 256)
	}

	pieces := SplitContent(data, "test.png", 102400, 20)
	if len(pieces) == 0 {
		t.Fatal("expected at least 1 piece")
	}

	// Each piece should be valid base64
	for i, p := range pieces {
		if len(p) == 0 {
			t.Errorf("piece[%d] is empty", i)
		}
	}
}

func TestSplitEmptyFile(t *testing.T) {
	pieces := SplitContent([]byte{}, "test.md", 102400, 20)
	if pieces != nil {
		t.Errorf("expected nil for empty file, got %v", pieces)
	}
}

func TestSplitLargeText(t *testing.T) {
	// Create text larger than pieceSize
	line := strings.Repeat("a", 100) + "\n"
	text := strings.Repeat(line, 2000) // ~200KB
	pieces := SplitContent([]byte(text), "test.md", 102400, 20)

	joined := strings.Join(pieces, "")
	if joined != text {
		t.Errorf("rejoin mismatch: lengths got %d, want %d", len(joined), len(text))
	}
}

func TestShouldSplitAsPlainText(t *testing.T) {
	tests := map[string]bool{
		"test.md":     true,
		"test.txt":    true,
		"test.canvas": true,
		"test.svg":    false,
		"test.html":   false,
		"test.png":    false,
	}
	for name, want := range tests {
		if got := ShouldSplitAsPlainText(name); got != want {
			t.Errorf("ShouldSplitAsPlainText(%q) = %v, want %v", name, got, want)
		}
	}
}
