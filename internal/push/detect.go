package push

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/woremacx/go-obsidian-livesync/internal/localdb"
	"github.com/woremacx/go-obsidian-livesync/internal/logw"
)

type mtimeUpdate struct {
	path  string
	mtime int64
	size  int64
}

type ChangedFile struct {
	Path    string // vault-relative path
	Action  string // "create", "update", "delete"
	Size    int64
	ModTime time.Time
}

// DetectChanges compares the vault directory against the vault_files table
// and returns a list of changed files. It always uses content hash comparison
// to reliably detect real changes and prevent echo loops.
func DetectChanges(store *localdb.Store, vaultPath string) ([]ChangedFile, error) {
	tracked, err := store.GetVaultFiles()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var changes []ChangedFile
	var mtimeUpdates []mtimeUpdate

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

		// Fast path: mtime and size unchanged — skip hash computation
		mtime := info.ModTime().UnixMilli()
		if rec.MTime == mtime && rec.Size == info.Size() {
			return nil
		}

		// mtime or size changed — compute content hash to check for real change
		data, err := os.ReadFile(path)
		if err != nil {
			logw.Warnf("read %s for hash: %v", relPath, err)
			return nil
		}
		h := sha256.Sum256(data)
		contentHash := hex.EncodeToString(h[:])

		if contentHash == rec.ContentHash {
			// Content identical — just update mtime/size metadata
			mtimeUpdates = append(mtimeUpdates, mtimeUpdate{path: relPath, mtime: mtime, size: info.Size()})
			return nil
		}

		// Hash differs — real change
		changes = append(changes, ChangedFile{
			Path:    relPath,
			Action:  "update",
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
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

	// Update mtime/size for files whose content hash matched
	for _, u := range mtimeUpdates {
		rec := tracked[u.path]
		store.UpsertVaultFile(u.path, rec.DocID, rec.Rev, rec.ContentHash, u.mtime, u.size)
	}

	return changes, nil
}
