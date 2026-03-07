package crypto

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/internal/types"
)

const encryptedMetaPrefix = "/\\:"

// IsEncryptedMeta checks if the path has /\: prefix (HKDF encrypted metadata).
func IsEncryptedMeta(path string) bool {
	return strings.HasPrefix(path, encryptedMetaPrefix)
}

// IsPathObfuscatedV2 checks for V2 path obfuscation (%/\).
func IsPathObfuscatedV2(path string) bool {
	return strings.HasPrefix(path, "%/\\")
}

// IsPathObfuscatedV1 checks for V1 path obfuscation (% prefix, length > 64).
func IsPathObfuscatedV1(path string) bool {
	return strings.HasPrefix(path, "%") && len(path) > 64 && !IsPathObfuscatedV2(path)
}

// DecryptPathMeta decrypts /\: encrypted metadata and returns the PathMetadata.
func DecryptPathMeta(path string, passphrase string, pbkdf2Salt []byte) (*types.PathMetadata, error) {
	if !IsEncryptedMeta(path) {
		return nil, fmt.Errorf("not encrypted metadata: %s", path[:min(len(path), 20)])
	}
	encrypted := path[len(encryptedMetaPrefix):]
	plainJSON, err := Decrypt(encrypted, passphrase, pbkdf2Salt, false)
	if err != nil {
		return nil, fmt.Errorf("decrypt path metadata: %w", err)
	}
	var meta types.PathMetadata
	if err := json.Unmarshal([]byte(plainJSON), &meta); err != nil {
		return nil, fmt.Errorf("parse path metadata JSON: %w", err)
	}
	return &meta, nil
}

// DecryptObfuscatedPathV1 decrypts a V1 obfuscated path.
// Format: %<IV hex 32><Salt hex 32><base64 ciphertext>
// Key derivation uses SHA-256(passphrase) + PBKDF2 with salt/IV derived from SHA-256(path+passphrase).
// For decryption, we use the IV and salt from the encoded path itself.
func DecryptObfuscatedPathV1(path string, passphrase string, dynamic bool) (string, error) {
	return decryptV1Hex(path, passphrase, dynamic)
}
