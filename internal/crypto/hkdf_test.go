package crypto

import (
	"strings"
	"testing"
)

func TestEncryptDecryptHKDF(t *testing.T) {
	passphrase := "testpassword"
	pbkdf2Salt := []byte("0123456789abcdef0123456789abcdef") // 32 bytes

	tests := []string{
		"hello world",
		"",
		"日本語テスト",
		"a longer string with special chars: !@#$%^&*()",
		strings.Repeat("x", 10000),
	}

	for _, plaintext := range tests {
		encrypted, err := EncryptHKDF(plaintext, passphrase, pbkdf2Salt)
		if err != nil {
			t.Fatalf("EncryptHKDF(%q): %v", truncTest(plaintext), err)
		}

		// Must start with "%="
		if !strings.HasPrefix(encrypted, "%=") {
			t.Errorf("encrypted should start with '%%=', got prefix %q", encrypted[:2])
		}

		// Decrypt should round-trip
		decrypted, err := Decrypt(encrypted, passphrase, pbkdf2Salt, false)
		if err != nil {
			t.Fatalf("Decrypt(%q): %v", truncTest(encrypted), err)
		}

		if decrypted != plaintext {
			t.Errorf("round-trip failed: got %q, want %q", truncTest(decrypted), truncTest(plaintext))
		}
	}
}

func truncTest(s string) string {
	if len(s) > 50 {
		return s[:50] + "..."
	}
	return s
}
