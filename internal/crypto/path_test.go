package crypto

import (
	"strings"
	"testing"

	"github.com/woremacx/go-obsidian-livesync/internal/types"
)

func TestEncryptDecryptPathMeta(t *testing.T) {
	passphrase := "testpassword"
	pbkdf2Salt := []byte("0123456789abcdef0123456789abcdef")

	meta := &types.PathMetadata{
		Path:     "notes/test.md",
		MTime:    1700000000000,
		CTime:    1700000000000,
		Size:     1234,
		Children: []string{"h:+abc", "h:+def"},
	}

	encrypted, err := EncryptPathMeta(meta, passphrase, pbkdf2Salt)
	if err != nil {
		t.Fatalf("EncryptPathMeta: %v", err)
	}

	if !strings.HasPrefix(encrypted, "/\\:") {
		t.Errorf("encrypted path should start with '/\\:', got %q", encrypted[:10])
	}

	// Decrypt round-trip
	decrypted, err := DecryptPathMeta(encrypted, passphrase, pbkdf2Salt)
	if err != nil {
		t.Fatalf("DecryptPathMeta: %v", err)
	}

	if decrypted.Path != meta.Path {
		t.Errorf("path: got %q, want %q", decrypted.Path, meta.Path)
	}
	if decrypted.MTime != meta.MTime {
		t.Errorf("mtime: got %d, want %d", decrypted.MTime, meta.MTime)
	}
	if len(decrypted.Children) != len(meta.Children) {
		t.Errorf("children count: got %d, want %d", len(decrypted.Children), len(meta.Children))
	}
}
