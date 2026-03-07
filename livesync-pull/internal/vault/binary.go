package vault

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Legacy UTF-16 encoding table: maps byte values to char codes.
// Bytes in range 0x26..0x7e (excluding 0x3a ':') are passed through.
// Other bytes are mapped to 0xc0 + index.
var revTable [0x200]int

func init() {
	// Build reverse table: charCode → byte value
	// The JS builds: table[i] = e (where e goes from 0xc0 to 0x1bf, i goes from 0 to 255)
	// revTable[e] = i
	for i := 0; i < 256; i++ {
		e := 0xc0 + i
		revTable[e] = i
	}
}

// decodeLegacyUTF16 decodes a single string from the legacy UTF-16 encoding.
// Each character's charCodeAt is mapped back to a byte.
func decodeLegacyUTF16(src string) []byte {
	out := make([]byte, len(src))
	for i, ch := range src {
		code := int(ch)
		if code >= 0x26 && code <= 0x7e && code != 0x3a {
			out[i] = byte(code)
		} else if code >= 0xc0 && code < 0x200 {
			out[i] = byte(revTable[code])
		} else {
			out[i] = byte(code & 0xff)
		}
	}
	return out
}

// DecodeBinary decodes binary content from its stored representation.
// If the data starts with %, it uses legacy UTF-16 decoding.
// Otherwise it uses base64 decoding.
// The data can be a single string or multiple strings (chunks joined).
func DecodeBinary(data string) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if strings.HasPrefix(data, "%") {
		return decodeLegacyUTF16(data[1:]), nil
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf("base64 decode binary: %w", err)
		}
	}
	return decoded, nil
}

// IsPlainText returns true if the filename is a known plain-text format.
func IsPlainText(filename string) bool {
	for _, ext := range []string{".md", ".txt", ".svg", ".html", ".csv", ".css", ".js", ".xml", ".canvas"} {
		if strings.HasSuffix(strings.ToLower(filename), ext) {
			return true
		}
	}
	return false
}
