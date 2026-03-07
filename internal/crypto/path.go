package crypto

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vrtmrz/obsidian-livesync/cmd/internal/logw"
	"github.com/vrtmrz/obsidian-livesync/cmd/internal/types"
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
	logw.Tracef("[pathMeta] encrypted_part prefix=%q len=%d", truncPathStr(encrypted, 30), len(encrypted))
	plainJSON, err := Decrypt(encrypted, passphrase, pbkdf2Salt, false)
	if err != nil {
		return nil, fmt.Errorf("decrypt path metadata: %w", err)
	}
	logw.Tracef("[pathMeta] decrypted JSON=%q", truncPathStr(plainJSON, 200))
	var meta types.PathMetadata
	if err := json.Unmarshal([]byte(plainJSON), &meta); err != nil {
		return nil, fmt.Errorf("parse path metadata JSON: %w (json=%q)", err, truncPathStr(plainJSON, 100))
	}
	return &meta, nil
}

// EncryptPathMeta encrypts PathMetadata as JSON, returns "/\:" + encrypted string.
func EncryptPathMeta(meta *types.PathMetadata, passphrase string, pbkdf2Salt []byte) (string, error) {
	jsonBytes, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("marshal path metadata: %w", err)
	}
	encrypted, err := EncryptHKDF(string(jsonBytes), passphrase, pbkdf2Salt)
	if err != nil {
		return "", fmt.Errorf("encrypt path metadata: %w", err)
	}
	return encryptedMetaPrefix + encrypted, nil
}

// DecryptObfuscatedPathV1 decrypts a V1 obfuscated path.
func DecryptObfuscatedPathV1(path string, passphrase string, dynamic bool) (string, error) {
	logw.Tracef("[pathV1] attempting V1 decrypt, path_len=%d", len(path))
	return decryptV1Hex(path, passphrase, dynamic)
}

func truncPathStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
