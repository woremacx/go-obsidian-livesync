package hash

import (
	"testing"
)

func TestMixedHash(t *testing.T) {
	// Test basic MixedHash with known seed and initial FNV
	// These values should match the JS implementation
	m, f := MixedHash("hello", 1, epochFNV1a)
	t.Logf("MixedHash('hello', 1, epochFNV1a) = (%d, %d)", m, f)

	// Verify MixedHash is deterministic
	m2, f2 := MixedHash("hello", 1, epochFNV1a)
	if m != m2 || f != f2 {
		t.Error("MixedHash not deterministic")
	}
}

func TestFallbackMixedHashEach(t *testing.T) {
	result := FallbackMixedHashEach("test")
	t.Logf("FallbackMixedHashEach('test') = %q", result)

	// Verify deterministic
	result2 := FallbackMixedHashEach("test")
	if result != result2 {
		t.Error("FallbackMixedHashEach not deterministic")
	}

	// Verify non-empty
	if len(result) == 0 {
		t.Error("FallbackMixedHashEach returned empty")
	}
}

func TestComputeHashedPassphrase(t *testing.T) {
	result := ComputeHashedPassphrase("testpassword")
	t.Logf("ComputeHashedPassphrase('testpassword') = %q", result)

	// Verify deterministic
	result2 := ComputeHashedPassphrase("testpassword")
	if result != result2 {
		t.Error("ComputeHashedPassphrase not deterministic")
	}
}

func TestComputeChunkID(t *testing.T) {
	hashedPass := ComputeHashedPassphrase("testpassword")
	id := ComputeChunkID("hello world", hashedPass)
	t.Logf("ComputeChunkID('hello world', hashedPass) = %q", id)

	// Must start with "h:+"
	if id[:3] != "h:+" {
		t.Errorf("ChunkID should start with 'h:+', got %q", id[:3])
	}

	// Verify deterministic
	id2 := ComputeChunkID("hello world", hashedPass)
	if id != id2 {
		t.Error("ComputeChunkID not deterministic")
	}
}

func TestPath2ID(t *testing.T) {
	id := Path2ID("notes/test.md", "testpassword")
	t.Logf("Path2ID('notes/test.md', 'testpassword') = %q", id)

	// Must start with "f:"
	if id[:2] != "f:" {
		t.Errorf("Path2ID should start with 'f:', got %q", id[:2])
	}

	// Verify deterministic
	id2 := Path2ID("notes/test.md", "testpassword")
	if id != id2 {
		t.Error("Path2ID not deterministic")
	}

	// Test path starting with "_"
	id3 := Path2ID("_internal/config.md", "testpassword")
	t.Logf("Path2ID('_internal/config.md', 'testpassword') = %q", id3)
	if id3[:2] != "f:" {
		t.Errorf("Path2ID should start with 'f:', got %q", id3[:2])
	}
}

func TestUtf16Len(t *testing.T) {
	// ASCII
	if utf16Len("hello") != 5 {
		t.Errorf("utf16Len('hello') = %d, want 5", utf16Len("hello"))
	}
	// Emoji (surrogate pair, 2 UTF-16 code units)
	if utf16Len("😀") != 2 {
		t.Errorf("utf16Len('😀') = %d, want 2", utf16Len("😀"))
	}
	// Japanese (1 UTF-16 code unit each)
	if utf16Len("日本") != 2 {
		t.Errorf("utf16Len('日本') = %d, want 2", utf16Len("日本"))
	}
}
