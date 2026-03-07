package push_test

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vrtmrz/obsidian-livesync/cmd/internal/hash"
	"github.com/vrtmrz/obsidian-livesync/cmd/internal/localdb"
	"github.com/vrtmrz/obsidian-livesync/cmd/internal/push"
	"github.com/vrtmrz/obsidian-livesync/cmd/internal/vault"
)

const (
	testPassphrase = "test-passphrase-12345"
)

var testPBKDF2Salt = []byte("0123456789abcdef0123456789abcdef") // 32 bytes

// TestRoundtripTextFiles tests push→pull roundtrip for plain text files.
func TestRoundtripTextFiles(t *testing.T) {
	files := map[string]string{
		"notes/hello.md":          "# Hello World\n\nThis is a test.\n",
		"notes/nested/deep.md":    "Deep nested file content.\n",
		"daily/2024-01-01.md":     "## Daily Note\n\n- Task 1\n- Task 2\n- Task 3\n",
		"test.txt":                "Plain text file.\n",
		"canvas/test.canvas":      `{"nodes":[],"edges":[]}`,
		"empty.md":                "",
		"日本語/テスト.md":             "日本語のコンテンツ\n",
		"emoji/😀.md":              "Emoji filename content\n",
		"long.md":                 strings.Repeat("This is a long line for testing chunk splitting behavior.\n", 500),
		"special chars & (1).md":  "File with special characters in name\n",
		"_internal/config.md":     "Underscore prefix path\n",
	}

	roundtrip(t, files)
}

// TestRoundtripBinaryFiles tests push→pull roundtrip for binary files.
func TestRoundtripBinaryFiles(t *testing.T) {
	// Small binary
	smallBin := make([]byte, 256)
	for i := range smallBin {
		smallBin[i] = byte(i)
	}

	// Large binary (> 100KB, will be multi-chunk)
	largeBin := make([]byte, 300_000)
	rand.Read(largeBin)

	// PNG-like header
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	pngData := append(pngHeader, largeBin[:1000]...)

	files := map[string][]byte{
		"attachments/small.bin":  smallBin,
		"attachments/large.bin":  largeBin,
		"images/test.png":        pngData,
	}

	roundtripBinary(t, files)
}

// TestRoundtripMixedFiles tests push→pull with both text and binary files together.
func TestRoundtripMixedFiles(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	restoreDir := t.TempDir()

	store, err := localdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	// Write source files
	textFiles := map[string]string{
		"notes/readme.md":   "# README\n\nMixed test.\n",
		"config.txt":        "key=value\n",
	}
	binFiles := map[string][]byte{
		"assets/icon.png": {0x89, 0x50, 0x4E, 0x47, 0x00, 0x01, 0x02, 0x03},
	}

	for path, content := range textFiles {
		writeTestFile(t, srcDir, path, []byte(content))
	}
	for path, content := range binFiles {
		writeTestFile(t, srcDir, path, content)
	}

	// Push all files to SQLite
	hashedPass := hash.ComputeHashedPassphrase(testPassphrase)
	allPaths := make([]string, 0)
	for path := range textFiles {
		allPaths = append(allPaths, path)
	}
	for path := range binFiles {
		allPaths = append(allPaths, path)
	}

	for _, path := range allPaths {
		info, _ := os.Stat(filepath.Join(srcDir, path))
		cf := push.ChangedFile{
			Path:    path,
			Action:  "create",
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}
		if err := push.PushToStore(store, cf, srcDir, testPassphrase, testPBKDF2Salt, hashedPass); err != nil {
			t.Fatalf("push %s: %v", path, err)
		}
	}

	// Clear vault_files so Materialize doesn't skip
	clearVaultFiles(t, store)

	// Materialize (pull) to restore directory
	docs, err := store.GetAllDocs()
	if err != nil {
		t.Fatalf("get all docs: %v", err)
	}
	stats, err := vault.Materialize(store, docs, restoreDir, testPassphrase, testPBKDF2Salt, false)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if stats.Errors > 0 {
		t.Errorf("materialize had %d errors", stats.Errors)
	}

	// Compare text files
	for path, expected := range textFiles {
		got, err := os.ReadFile(filepath.Join(restoreDir, path))
		if err != nil {
			t.Errorf("read restored %s: %v", path, err)
			continue
		}
		if string(got) != expected {
			t.Errorf("%s: content mismatch\n  got:  %q\n  want: %q", path, truncate(string(got), 100), truncate(expected, 100))
		}
	}

	// Compare binary files
	for path, expected := range binFiles {
		got, err := os.ReadFile(filepath.Join(restoreDir, path))
		if err != nil {
			t.Errorf("read restored %s: %v", path, err)
			continue
		}
		if !bytes.Equal(got, expected) {
			t.Errorf("%s: binary content mismatch (got %d bytes, want %d bytes)", path, len(got), len(expected))
		}
	}
}

// TestRoundtripDetectAndPush tests the full flow: detect changes → push → pull.
func TestRoundtripDetectAndPush(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	restoreDir := t.TempDir()

	store, err := localdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	// Write source files
	files := map[string]string{
		"a.md": "File A\n",
		"b.md": "File B\n",
		"c.md": "File C\n",
	}
	for path, content := range files {
		writeTestFile(t, srcDir, path, []byte(content))
	}

	// Detect changes (all should be "create" since vault_files is empty)
	changes, err := push.DetectChanges(store, srcDir, false)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(changes) != len(files) {
		t.Fatalf("expected %d changes, got %d", len(files), len(changes))
	}
	for _, c := range changes {
		if c.Action != "create" {
			t.Errorf("expected action=create for %s, got %s", c.Path, c.Action)
		}
	}

	// Push
	hashedPass := hash.ComputeHashedPassphrase(testPassphrase)
	for _, c := range changes {
		if err := push.PushToStore(store, c, srcDir, testPassphrase, testPBKDF2Salt, hashedPass); err != nil {
			t.Fatalf("push %s: %v", c.Path, err)
		}
	}

	// Detect again — should find no changes since vault_files is now populated
	changes2, err := push.DetectChanges(store, srcDir, false)
	if err != nil {
		t.Fatalf("detect after push: %v", err)
	}
	if len(changes2) != 0 {
		t.Errorf("expected 0 changes after push, got %d", len(changes2))
		for _, c := range changes2 {
			t.Logf("  unexpected change: %s %s", c.Action, c.Path)
		}
	}

	// Modify a file
	time.Sleep(10 * time.Millisecond) // ensure mtime differs
	writeTestFile(t, srcDir, "b.md", []byte("File B updated\n"))
	files["b.md"] = "File B updated\n"

	// Delete a file
	os.Remove(filepath.Join(srcDir, "c.md"))
	delete(files, "c.md")

	// Detect changes — should find 1 update + 1 delete
	changes3, err := push.DetectChanges(store, srcDir, false)
	if err != nil {
		t.Fatalf("detect after modify: %v", err)
	}
	actionMap := map[string]string{}
	for _, c := range changes3 {
		actionMap[c.Path] = c.Action
	}
	if actionMap["b.md"] != "update" {
		t.Errorf("expected b.md update, got %v", actionMap["b.md"])
	}
	if actionMap["c.md"] != "delete" {
		t.Errorf("expected c.md delete, got %v", actionMap["c.md"])
	}

	// Push changes
	for _, c := range changes3 {
		if err := push.PushToStore(store, c, srcDir, testPassphrase, testPBKDF2Salt, hashedPass); err != nil {
			t.Fatalf("push change %s %s: %v", c.Action, c.Path, err)
		}
	}

	// Clear vault_files for Materialize
	clearVaultFiles(t, store)

	// Materialize to restore dir
	docs, err := store.GetAllDocs()
	if err != nil {
		t.Fatalf("get all docs: %v", err)
	}
	stats, err := vault.Materialize(store, docs, restoreDir, testPassphrase, testPBKDF2Salt, false)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if stats.Errors > 0 {
		t.Errorf("materialize had %d errors", stats.Errors)
	}

	// Verify: a.md and b.md should exist with correct content, c.md should not
	for path, expected := range files {
		got, err := os.ReadFile(filepath.Join(restoreDir, path))
		if err != nil {
			t.Errorf("read restored %s: %v", path, err)
			continue
		}
		if string(got) != expected {
			t.Errorf("%s: content mismatch\n  got:  %q\n  want: %q", path, string(got), expected)
		}
	}

	// c.md should NOT exist
	if _, err := os.Stat(filepath.Join(restoreDir, "c.md")); err == nil {
		t.Error("c.md should have been deleted but still exists")
	}

	t.Logf("stats: written=%d deleted=%d unchanged=%d skipped=%d errors=%d",
		stats.Written, stats.Deleted, stats.Unchanged, stats.Skipped, stats.Errors)
}

// --- helpers ---

func roundtrip(t *testing.T, files map[string]string) {
	t.Helper()
	srcDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	restoreDir := t.TempDir()

	store, err := localdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	// Write source files
	for path, content := range files {
		writeTestFile(t, srcDir, path, []byte(content))
	}

	// Push to SQLite
	hashedPass := hash.ComputeHashedPassphrase(testPassphrase)
	for path := range files {
		info, _ := os.Stat(filepath.Join(srcDir, path))
		cf := push.ChangedFile{
			Path:    path,
			Action:  "create",
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}
		if err := push.PushToStore(store, cf, srcDir, testPassphrase, testPBKDF2Salt, hashedPass); err != nil {
			t.Fatalf("push %s: %v", path, err)
		}
	}

	// Clear vault_files tracking so Materialize doesn't skip
	clearVaultFiles(t, store)

	// Materialize (pull) to restore directory
	docs, err := store.GetAllDocs()
	if err != nil {
		t.Fatalf("get all docs: %v", err)
	}

	stats, err := vault.Materialize(store, docs, restoreDir, testPassphrase, testPBKDF2Salt, false)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	if stats.Errors > 0 {
		t.Errorf("materialize had %d errors", stats.Errors)
	}

	// Compare
	for path, expected := range files {
		got, err := os.ReadFile(filepath.Join(restoreDir, path))
		if err != nil {
			t.Errorf("read restored %s: %v", path, err)
			continue
		}
		if string(got) != expected {
			t.Errorf("%s: content mismatch\n  got len=%d: %q\n  want len=%d: %q",
				path, len(got), truncate(string(got), 200), len(expected), truncate(expected, 200))
		}
	}

	t.Logf("roundtrip OK: %d files, written=%d unchanged=%d skipped=%d errors=%d",
		len(files), stats.Written, stats.Unchanged, stats.Skipped, stats.Errors)
}

func roundtripBinary(t *testing.T, files map[string][]byte) {
	t.Helper()
	srcDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	restoreDir := t.TempDir()

	store, err := localdb.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	for path, content := range files {
		writeTestFile(t, srcDir, path, content)
	}

	hashedPass := hash.ComputeHashedPassphrase(testPassphrase)
	for path := range files {
		info, _ := os.Stat(filepath.Join(srcDir, path))
		cf := push.ChangedFile{
			Path:    path,
			Action:  "create",
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}
		if err := push.PushToStore(store, cf, srcDir, testPassphrase, testPBKDF2Salt, hashedPass); err != nil {
			t.Fatalf("push %s: %v", path, err)
		}
	}

	clearVaultFiles(t, store)

	docs, err := store.GetAllDocs()
	if err != nil {
		t.Fatalf("get all docs: %v", err)
	}

	stats, err := vault.Materialize(store, docs, restoreDir, testPassphrase, testPBKDF2Salt, false)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}

	if stats.Errors > 0 {
		t.Errorf("materialize had %d errors", stats.Errors)
	}

	for path, expected := range files {
		got, err := os.ReadFile(filepath.Join(restoreDir, path))
		if err != nil {
			t.Errorf("read restored %s: %v", path, err)
			continue
		}
		if !bytes.Equal(got, expected) {
			t.Errorf("%s: binary content mismatch (got %d bytes, want %d bytes)", path, len(got), len(expected))
		}
	}

	t.Logf("roundtrip OK: %d binary files, written=%d errors=%d",
		len(files), stats.Written, stats.Errors)
}

func writeTestFile(t *testing.T, dir, relPath string, content []byte) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, content, 0644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func clearVaultFiles(t *testing.T, store *localdb.Store) {
	t.Helper()
	vf, err := store.GetVaultFiles()
	if err != nil {
		t.Fatalf("get vault files: %v", err)
	}
	for path := range vf {
		store.DeleteVaultFile(path)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
