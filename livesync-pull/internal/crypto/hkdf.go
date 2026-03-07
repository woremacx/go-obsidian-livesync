package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/pbkdf2"
)

const (
	ivLength         = 12
	hkdfSaltLength   = 32
	pbkdf2SaltLength = 32
	pbkdf2Iterations = 310000
)

// deriveMasterKeyHKDF derives the HKDF master key from passphrase and PBKDF2 salt.
// Returns raw key bytes (32 bytes).
func deriveMasterKeyHKDF(passphrase string, pbkdf2Salt []byte) []byte {
	passBytes := []byte(passphrase)
	return pbkdf2.Key(passBytes, pbkdf2Salt, pbkdf2Iterations, 32, sha256.New)
}

// deriveChunkKey derives an AES-256 key from the master key and HKDF salt.
func deriveChunkKey(masterKey, hkdfSalt []byte) ([]byte, error) {
	r := hkdf.New(sha256.New, masterKey, hkdfSalt, nil)
	key := make([]byte, 32)
	if _, err := r.Read(key); err != nil {
		return nil, fmt.Errorf("hkdf expand: %w", err)
	}
	return key, nil
}

// decryptAESGCM decrypts data using AES-256-GCM with the given key and IV.
// Supports both standard 12-byte and non-standard (e.g. 16-byte for V1) nonce sizes.
func decryptAESGCM(key, iv, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	var gcm cipher.AEAD
	if len(iv) == 12 {
		gcm, err = cipher.NewGCM(block)
	} else {
		gcm, err = cipher.NewGCMWithNonceSize(block, len(iv))
	}
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("AES-GCM decrypt failed: %w", err)
	}
	return plaintext, nil
}

// decryptHKDF decrypts HKDF-encrypted data (after removing the %=  prefix).
// The data is base64-encoded: IV(12) | hkdfSalt(32) | ciphertext+tag
func decryptHKDF(b64data string, passphrase string, pbkdf2Salt []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64data)
	if err != nil {
		// Try RawStdEncoding (no padding)
		raw, err = base64.RawStdEncoding.DecodeString(b64data)
		if err != nil {
			return "", fmt.Errorf("base64 decode: %w", err)
		}
	}
	if len(raw) < ivLength+hkdfSaltLength+1 {
		return "", fmt.Errorf("HKDF data too short: %d bytes", len(raw))
	}
	iv := raw[:ivLength]
	salt := raw[ivLength : ivLength+hkdfSaltLength]
	ciphertext := raw[ivLength+hkdfSaltLength:]

	masterKey := deriveMasterKeyHKDF(passphrase, pbkdf2Salt)
	chunkKey, err := deriveChunkKey(masterKey, salt)
	if err != nil {
		return "", err
	}
	plaintext, err := decryptAESGCM(chunkKey, iv, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// decryptHKDFEphemeral decrypts %$ ephemeral-salt data.
// Binary: pbkdf2Salt(32) | IV(12) | hkdfSalt(32) | ciphertext+tag
func decryptHKDFEphemeral(b64data string, passphrase string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64data)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(b64data)
		if err != nil {
			return "", fmt.Errorf("base64 decode: %w", err)
		}
	}
	minLen := pbkdf2SaltLength + ivLength + hkdfSaltLength + 1
	if len(raw) < minLen {
		return "", fmt.Errorf("ephemeral data too short: %d bytes", len(raw))
	}
	p2salt := raw[:pbkdf2SaltLength]
	iv := raw[pbkdf2SaltLength : pbkdf2SaltLength+ivLength]
	hSalt := raw[pbkdf2SaltLength+ivLength : pbkdf2SaltLength+ivLength+hkdfSaltLength]
	ciphertext := raw[pbkdf2SaltLength+ivLength+hkdfSaltLength:]

	masterKey := deriveMasterKeyHKDF(passphrase, p2salt)
	chunkKey, err := deriveChunkKey(masterKey, hSalt)
	if err != nil {
		return "", err
	}
	plaintext, err := decryptAESGCM(chunkKey, iv, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
