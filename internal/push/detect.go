package push

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vrtmrz/obsidian-livesync/cmd/internal/localdb"
	"github.com/vrtmrz/obsidian-livesync/cmd/internal/logw"
)

type ChangedFile struct {
	Path    string // vault-relative path
	Action  string // "create", "update", "delete"
	Size    int64
	ModTime time.Time
}

// DetectChanges compares the vault directory against the vault_files table
// and returns a list of changed files.
func DetectChanges(store *localdb.Store, vaultPath string, forceContentHash bool) ([]ChangedFile, error) {
	tracked, err := store.GetVaultFiles()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var changes []ChangedFile

	err = filepath.Walk(vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(vaultPath, path)
		if err != nil {
			return nil
		}
		// Normalize to forward slashes
		relPath = filepath.ToSlash(relPath)

		// Skip hidden files
		if strings.HasPrefix(filepath.Base(relPath), ".") {
			return nil
		}

		seen[relPath] = true

		rec, exists := tracked[relPath]
		if !exists {
			changes = append(changes, ChangedFile{
				Path:    relPath,
				Action:  "create",
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
			return nil
		}

		// Check mtime and size
		mtime := info.ModTime().UnixMilli()
		if rec.MTime == mtime && rec.Size == info.Size() && !forceContentHash {
			return nil
		}

		// Content hash comparison for potential updates
		if forceContentHash || rec.Size != info.Size() {
			changes = append(changes, ChangedFile{
				Path:    relPath,
				Action:  "update",
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
			return nil
		}

		// mtime differs but size same — check content hash
		data, err := os.ReadFile(path)
		if err != nil {
			logw.Warnf("read %s for hash: %v", relPath, err)
			return nil
		}
		h := sha256.Sum256(data)
		contentHash := hex.EncodeToString(h[:])
		if contentHash != rec.ContentHash {
			changes = append(changes, ChangedFile{
				Path:    relPath,
				Action:  "update",
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Detect deletions: tracked files not found on disk
	for path := range tracked {
		if !seen[path] {
			changes = append(changes, ChangedFile{
				Path:   path,
				Action: "delete",
			})
		}
	}

	return changes, nil
}
