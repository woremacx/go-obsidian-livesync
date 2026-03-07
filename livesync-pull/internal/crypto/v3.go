package crypto

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/pbkdf2"
)

const v3FixedSalt = "fancySyncForYou!"

// decryptV3 decrypts V3 format: %~<IV hex 24><base64 ciphertext>
func decryptV3(data string, passphrase string) (string, error) {
	if len(data) < 28 {
		return "", fmt.Errorf("V3 data too short: %d chars", len(data))
	}
	ivHex := data[2:26]
	cipherB64 := data[26:]

	iv, err := hex.DecodeString(ivHex)
	if err != nil {
		return "", fmt.Errorf("V3 IV decode: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		ciphertext, err = base64.RawStdEncoding.DecodeString(cipherB64)
		if err != nil {
			return "", fmt.Errorf("V3 ciphertext decode: %w", err)
		}
	}

	key := deriveV3Key(passphrase)
	plaintext, err := decryptAESGCM(key, iv, ciphertext)
	if err != nil {
		return "", fmt.Errorf("V3 decrypt: %w", err)
	}
	return string(plaintext), nil
}

// deriveV3Key derives the AES-256 key for V3 encryption.
// salt = SHA-256(passphrase + "fancySyncForYou!")[0:16]
// key = PBKDF2(passphrase, salt, 100000, SHA-256)
func deriveV3Key(passphrase string) []byte {
	passBytes := []byte(passphrase)
	saltInput := append(passBytes, []byte(v3FixedSalt)...)
	digest := sha256.Sum256(saltInput)
	salt := digest[:16]
	return pbkdf2.Key(passBytes, salt, 100000, 32, sha256.New)
}
