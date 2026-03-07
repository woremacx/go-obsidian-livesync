package hash

import (
	"math"
	"strconv"
	"unicode/utf16"
)

const (
	epochFNV1a = uint32(2166136261)
	saltOfID   = "a83hrf7f\x03y7sa8g31"
	jsC1       = float64(0xcc9e2d51)
	jsC2       = float64(0x1b873593)
)

// utf16CodeUnits returns the UTF-16 code units of a Go string,
// matching JS str.charCodeAt(i) behavior.
func utf16CodeUnits(s string) []uint16 {
	return utf16.Encode([]rune(s))
}

// utf16Len returns the JS .length of a string (UTF-16 code unit count).
func utf16Len(s string) int {
	return len(utf16CodeUnits(s))
}

// jsToInt32 replicates JS ToInt32: truncate float64, mod 2^32, signed.
func jsToInt32(f float64) int32 {
	if math.IsNaN(f) || math.IsInf(f, 0) || f == 0 {
		return 0
	}
	n := math.Trunc(f)
	mod := math.Mod(n, 4294967296)
	if mod < 0 {
		mod += 4294967296
	}
	if mod >= 2147483648 {
		return int32(mod - 4294967296)
	}
	return int32(mod)
}

// imul replicates Math.imul: 32-bit integer multiplication.
func imul(a, b uint32) uint32 {
	return uint32(int32(a) * int32(b))
}

// MixedHash computes MurmurHash3 + FNV-1a on UTF-16 code units of str.
// Replicates JS behavior exactly, including float64 precision effects
// in the inner loop (JS uses *= not Math.imul for k1*c1, k1*c2, h1*5+n).
func MixedHash(str string, seed uint32, fnv1aHash uint32) (uint32, uint32) {
	units := utf16CodeUnits(str)
	fnv := fnv1aHash
	length := len(units)

	// h1 is tracked as float64 to match JS behavior between iterations.
	// JS: h1 = h1 * 5 + n produces a float64 that gets truncated to int32
	// at the next XOR.
	h1 := float64(seed)

	for _, u := range units {
		k1 := float64(u)

		// FNV-1a (pure uint32 operations, no float64 precision issues)
		fnv ^= uint32(u)
		fnv = imul(fnv, 0x01000193)

		// MurmurHash3 inner loop — JS uses float64 *= not Math.imul
		// Step 1: k1 *= c1 (float64 multiply, exact for charCode values < 2^16)
		k1 *= jsC1

		// Step 2: rotate (truncates to int32 via bitwise ops)
		k1i := jsToInt32(k1)
		k1u := uint32(k1i)
		k1u = (k1u << 15) | (k1u >> 17)
		k1i = int32(k1u)

		// Step 3: k1 *= c2 (float64 multiply, may lose precision for large k1)
		k1f := float64(k1i) * jsC2

		// Step 4: h1 ^= k1 (both converted to int32)
		h1i := jsToInt32(h1) ^ jsToInt32(k1f)

		// Step 5: rotate h1
		h1u := uint32(h1i)
		h1u = (h1u << 13) | (h1u >> 19)

		// Step 6: h1 = h1 * 5 + n (float64, exact since result < 2^34)
		h1 = float64(int32(h1u))*5.0 + float64(0xe6546b64)
	}

	// Finalization (uses Math.imul — correct 32-bit multiply)
	h1u := uint32(jsToInt32(h1)) ^ uint32(length)
	h1u ^= h1u >> 16
	h1u = imul(h1u, 0x85ebca6b)
	h1u ^= h1u >> 13
	h1u = imul(h1u, 0xc2b2ae35)
	h1u ^= h1u >> 16

	return h1u, fnv
}

// FallbackMixedHashEach computes the hash used by LiveSync for passphrase hashing.
// JS: fallbackMixedHashEach(src) = mixedHash(src.length + src, 1, epochFNV1a)
// then m.toString(36) + f.toString(36)
func FallbackMixedHashEach(src string) string {
	srcLen := utf16Len(src)
	input := strconv.Itoa(srcLen) + src
	m, f := MixedHash(input, 1, epochFNV1a)
	return strconv.FormatUint(uint64(m), 36) + strconv.FormatUint(uint64(f), 36)
}

// ComputeHashedPassphrase derives the hashed passphrase used for chunk ID generation.
func ComputeHashedPassphrase(passphrase string) string {
	units := utf16CodeUnits(passphrase)
	usingLetters := (len(units) / 4) * 3
	if usingLetters > len(units) {
		usingLetters = len(units)
	}
	substr := string(utf16.Decode(units[:usingLetters]))
	passphraseForHash := saltOfID + substr
	return FallbackMixedHashEach(passphraseForHash)
}
