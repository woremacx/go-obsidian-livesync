package crypto

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/logw"
	"golang.org/x/crypto/pbkdf2"
)

// jsStringLength returns the JS .length of a string (UTF-16 code unit count).
// This differs from Go's len() which counts UTF-8 bytes.
func jsStringLength(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// v1Iterations calculates the iteration count for V1 encryption.
// JS: passphraseLen = 15 - passphrase.length  (UTF-16 code units!)
//
//	(passphraseLen > 0 ? passphraseLen : 0) * 1000 + 121 - passphraseLen
func v1Iterations(passphrase string, dynamic bool) int {
	if !dynamic {
		return 100000
	}
	passphraseLen := 15 - jsStringLength(passphrase)
	clamped := passphraseLen
	if clamped < 0 {
		clamped = 0
	}
	return clamped*1000 + 121 - passphraseLen
}

// deriveV1Key derives the AES-256 key for V1 encryption.
// V1 uses SHA-256(passphrase) as the key material, then PBKDF2 with the salt.
func deriveV1Key(passphrase string, salt []byte, dynamic bool) []byte {
	h := sha256.Sum256([]byte(passphrase))
	iter := v1Iterations(passphrase, dynamic)
	logw.Tracef("[v1key] iterations=%d, salt_hex=%x, digest_hex=%x", iter, salt, h[:8])
	return pbkdf2.Key(h[:], salt, iter, 32, sha256.New)
}

// decryptV1Hex decrypts V1-Hex format: %<IV hex 32><Salt hex 32><base64 ciphertext>
func decryptV1Hex(data string, passphrase string, dynamic bool) (string, error) {
	if len(data) < 66 {
		return "", fmt.Errorf("V1-Hex data too short: %d chars", len(data))
	}
	ivHex := data[1:33]
	saltHex := data[33:65]
	cipherB64 := data[65:]

	logw.Tracef("[v1hex] ivHex=%s saltHex=%s cipher_b64_len=%d", ivHex, saltHex, len(cipherB64))

	iv, err := hex.DecodeString(ivHex)
	if err != nil {
		return "", fmt.Errorf("V1-Hex IV decode: %w", err)
	}
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return "", fmt.Errorf("V1-Hex salt decode: %w", err)
	}
	ciphertext, err := decodeBinaryB64(cipherB64)
	if err != nil {
		return "", fmt.Errorf("V1-Hex ciphertext decode: %w", err)
	}

	logw.Tracef("[v1hex] iv_len=%d salt_len=%d ciphertext_len=%d", len(iv), len(salt), len(ciphertext))

	key := deriveV1Key(passphrase, salt, dynamic)
	plaintext, err := decryptAESGCM(key, iv, ciphertext)
	if err != nil {
		// Fallback: try opposite dynamic iteration mode
		key2 := deriveV1Key(passphrase, salt, !dynamic)
		plaintext2, err2 := decryptAESGCM(key2, iv, ciphertext)
		if err2 != nil {
			return "", fmt.Errorf("V1-Hex decrypt: %w (fallback also failed)", err)
		}
		return string(plaintext2), nil
	}
	return string(plaintext), nil
}

// decryptV1JSON decrypts V1-JSON format: ["base64cipher","ivHex","saltHex"]
// The decrypted result needs an additional JSON.parse (the plaintext is JSON-stringified).
func decryptV1JSON(data string, passphrase string, dynamic bool) (string, error) {
	// Parse as JSON array manually (simple split approach matching the JS)
	inner := data[1 : len(data)-1]
	parts := strings.Split(inner, ",")
	if len(parts) != 3 {
		return "", fmt.Errorf("V1-JSON: expected 3 parts, got %d", len(parts))
	}
	// Strip quotes
	encData := stripQuotes(parts[0])
	ivHex := stripQuotes(parts[1])
	saltHex := stripQuotes(parts[2])

	logw.Tracef("[v1json] ivHex=%s saltHex=%s enc_len=%d", ivHex, saltHex, len(encData))

	iv, err := hex.DecodeString(ivHex)
	if err != nil {
		return "", fmt.Errorf("V1-JSON IV decode: %w", err)
	}
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return "", fmt.Errorf("V1-JSON salt decode: %w", err)
	}
	// V1-JSON uses atob for base64 decode (standard base64)
	ciphertext, err := base64.StdEncoding.DecodeString(encData)
	if err != nil {
		// Try without padding
		ciphertext, err = base64.RawStdEncoding.DecodeString(encData)
		if err != nil {
			return "", fmt.Errorf("V1-JSON ciphertext decode: %w", err)
		}
	}

	key := deriveV1Key(passphrase, salt, dynamic)
	plaintext, err := decryptAESGCM(key, iv, ciphertext)
	if err != nil {
		// Fallback: try opposite dynamic iteration mode
		key2 := deriveV1Key(passphrase, salt, !dynamic)
		plaintext2, err2 := decryptAESGCM(key2, iv, ciphertext)
		if err2 != nil {
			return "", fmt.Errorf("V1-JSON decrypt: %w (fallback also failed)", err)
		}
		plaintext = plaintext2
	}

	// V1-JSON wraps content in JSON.stringify, so we need to JSON.parse
	var result string
	if err := json.Unmarshal(plaintext, &result); err != nil {
		return "", fmt.Errorf("V1-JSON JSON.parse: %w (plaintext first 100: %q)", err, truncStr100(plaintext))
	}
	return result, nil
}

func stripQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// decodeBinaryB64 decodes base64 data the same way the JS `decodeBinary` does for V1-Hex.
// The V1-Hex path uses decodeBinary(encryptedData) which calls base64ToArrayBuffer.
func decodeBinaryB64(b64 string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(b64)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func truncStr100(b []byte) string {
	if len(b) <= 100 {
		return string(b)
	}
	return string(b[:100])
}
