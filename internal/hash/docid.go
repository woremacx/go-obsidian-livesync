package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// hashStringSHA256 computes hex(SHA-256(UTF-8(key))).
// This matches the JS hashString function (the stretching loop is a no-op).
func hashStringSHA256(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// Path2ID generates a CouchDB document ID for a file path with path obfuscation.
// JS: prefix + "f:" + hashString(hashString(passphrase) + ":" + filename)
// For paths starting with "_", a leading "/" is prepended.
// For paths containing ":", the prefix before ":" is preserved.
func Path2ID(path string, passphrase string) string {
	filename := path

	x := filename
	if strings.HasPrefix(x, "_") {
		x = "/" + x
	}

	// expandFilePathPrefix: split on first ":"
	prefix := ""
	if idx := strings.Index(x, ":"); idx >= 0 {
		prefix = x[:idx+1]
		x = x[idx+1:]
	}

	hashedPassphrase := hashStringSHA256(passphrase)
	out := hashStringSHA256(hashedPassphrase + ":" + filename)
	return prefix + "f:" + out
}
