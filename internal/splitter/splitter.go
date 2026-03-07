package splitter

import (
	"encoding/base64"
	"strings"
	"unicode/utf16"
)

const (
	DefaultPieceSize    = 102400
	DefaultMinChunkSize = 20
	maxItems            = 100
)

// ShouldSplitAsPlainText returns true for file types that should be split by newlines.
func ShouldSplitAsPlainText(filename string) bool {
	lower := strings.ToLower(filename)
	for _, ext := range []string{".md", ".txt", ".canvas"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// IsPlainText returns true if the filename is a known plain-text format.
func IsPlainText(filename string) bool {
	lower := strings.ToLower(filename)
	for _, ext := range []string{".md", ".txt", ".svg", ".html", ".csv", ".css", ".js", ".xml", ".canvas"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// SplitContent splits file data into chunks compatible with LiveSync's V2 splitter.
// For plain-text split files (.md, .txt, .canvas): split by newlines, merge small chunks, cap at pieceSize.
// For other text files: cap at pieceSize (in UTF-16 code units to match JS behavior).
// For binary files: split raw bytes, base64-encode each chunk.
func SplitContent(data []byte, filename string, pieceSize int, minChunkSize int) []string {
	if len(data) == 0 {
		return nil
	}
	if pieceSize <= 0 {
		pieceSize = DefaultPieceSize
	}
	if minChunkSize <= 0 {
		minChunkSize = DefaultMinChunkSize
	}

	if IsPlainText(filename) {
		text := string(data)
		if ShouldSplitAsPlainText(filename) {
			return splitPlainText(text, pieceSize, minChunkSize)
		}
		return splitTextBySize(text, pieceSize)
	}
	return splitBinary(data, pieceSize)
}

// splitPlainText splits text by newlines with minimum chunk size, then caps at pieceSize.
// Matches JS: splitByDelimiterWithMinLength(stringGenerator([text]), "\n", xMinimumChunkSize)
// then chunkStringGeneratorFromGenerator(gen1, pieceSize).
func splitPlainText(text string, pieceSize int, minChunkSize int) []string {
	textLen := utf16Len(text)

	// Adjust minimum chunk size to keep chunk count reasonable
	xMinChunkSize := minChunkSize
	for textLen/xMinChunkSize > maxItems {
		xMinChunkSize += minChunkSize
	}

	// Phase 1: Split by newlines with minimum chunk length
	lines := strings.SplitAfter(text, "\n")
	var merged []string
	buf := ""
	for _, line := range lines {
		buf += line
		if utf16Len(buf) > xMinChunkSize {
			merged = append(merged, buf)
			buf = ""
		}
	}
	if buf != "" {
		merged = append(merged, buf)
	}

	// Phase 2: Cap each piece at pieceSize
	var result []string
	for _, piece := range merged {
		result = append(result, chunkString(piece, pieceSize)...)
	}
	return result
}

// splitTextBySize splits text into chunks of at most pieceSize UTF-16 code units.
// Avoids splitting surrogate pairs.
func splitTextBySize(text string, pieceSize int) []string {
	return chunkString(text, pieceSize)
}

// chunkString splits a string into chunks of at most maxLen UTF-16 code units,
// without splitting surrogate pairs.
func chunkString(s string, maxLen int) []string {
	units := utf16.Encode([]rune(s))
	if len(units) <= maxLen {
		return []string{s}
	}

	var result []string
	from := 0
	for from < len(units) {
		end := from + maxLen
		if end > len(units) {
			end = len(units)
		}
		// Don't split in the middle of a surrogate pair
		for end < len(units) && end > from && isLowSurrogate(units[end-1]) && end-1 > from && isHighSurrogate(units[end-2]) {
			// The last code unit is a low surrogate preceded by a high surrogate.
			// This is fine — the pair is complete. But if the unit AT end is a high surrogate
			// (start of next pair), we should not split there.
			break
		}
		if end < len(units) && isHighSurrogate(units[end-1]) {
			// Ends with an unpaired high surrogate — include the low surrogate too
			end++
		}
		chunk := string(utf16.Decode(units[from:end]))
		result = append(result, chunk)
		from = end
	}
	return result
}

func isHighSurrogate(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }
func isLowSurrogate(u uint16) bool  { return u >= 0xDC00 && u <= 0xDFFF }

// splitBinary splits binary data into chunks at delimiter boundaries, base64-encodes each.
// Matches JS V2 binary splitter behavior.
func splitBinary(data []byte, pieceSize int) []string {
	// For binary, compute a reasonable minimum chunk size based on data size
	clampMin := 100000 // 100kb
	clampMax := 100000000
	clampedSize := len(data)
	if clampedSize < clampMin {
		clampedSize = clampMin
	}
	if clampedSize > clampMax {
		clampedSize = clampMax
	}
	step := 1
	w := clampedSize
	for w > 10 {
		w /= 12 // Approximate JS w /= 12.5
		step++
	}
	minChunkSize := 1
	for i := 0; i < step-1; i++ {
		minChunkSize *= 10
	}

	var result []string
	i := 0
	size := len(data)
	for i < size {
		findStart := i + minChunkSize
		defaultEnd := i + pieceSize
		splitEnd := defaultEnd

		if findStart < size {
			// Find null byte or newline after minimum chunk size
			idx := indexByteFrom(data, 0x00, findStart)
			if idx == -1 {
				idx = indexByteFrom(data, '\n', findStart)
			}
			if idx != -1 && idx < defaultEnd {
				splitEnd = idx
			}
		}
		if splitEnd > size {
			splitEnd = size
		}

		chunk := base64.StdEncoding.EncodeToString(data[i:splitEnd])
		result = append(result, chunk)
		i = splitEnd
	}
	return result
}

func indexByteFrom(data []byte, b byte, from int) int {
	if from >= len(data) {
		return -1
	}
	for i := from; i < len(data); i++ {
		if data[i] == b {
			return i
		}
	}
	return -1
}

func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}
