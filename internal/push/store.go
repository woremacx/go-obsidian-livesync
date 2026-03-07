package push

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vrtmrz/obsidian-livesync/cmd/internal/crypto"
	"github.com/vrtmrz/obsidian-livesync/cmd/internal/hash"
	"github.com/vrtmrz/obsidian-livesync/cmd/internal/localdb"
	"github.com/vrtmrz/obsidian-livesync/cmd/internal/splitter"
	"github.com/vrtmrz/obsidian-livesync/cmd/internal/types"
)

// PushToStore pushes a single file into the local SQLite store (no CouchDB).
// This produces the same document format as PushFile but writes directly to SQLite,
// suitable for testing the push→pull roundtrip without a CouchDB server.
func PushToStore(store *localdb.Store, file ChangedFile,
	vaultPath, passphrase string, pbkdf2Salt []byte, hashedPassphrase string) error {

	if file.Action == "delete" {
		return pushDeleteToStore(store, file, passphrase)
	}

	fullPath := filepath.Join(vaultPath, file.Path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	pieces := splitter.SplitContent(data, file.Path, splitter.DefaultPieceSize, splitter.DefaultMinChunkSize)
	if len(pieces) == 0 {
		pieces = []string{""}
	}

	var children []string
	for _, piece := range pieces {
		chunkID := hash.ComputeChunkID(piece, hashedPassphrase)
		children = append(children, chunkID)

		encrypted, err := crypto.EncryptHKDF(piece, passphrase, pbkdf2Salt)
		if err != nil {
			return fmt.Errorf("encrypt chunk %s: %w", chunkID, err)
		}

		leafDoc := map[string]interface{}{
			"_id":  chunkID,
			"type": "leaf",
			"data": encrypted,
			"e_":   true,
		}
		leafJSON, _ := json.Marshal(leafDoc)
		rev := "1-" + shortHash(leafJSON)
		if err := store.UpsertDoc(chunkID, rev, leafJSON, false); err != nil {
			return fmt.Errorf("upsert leaf %s: %w", chunkID, err)
		}
	}

	docID := hash.Path2ID(file.Path, passphrase)

	info, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	meta := &types.PathMetadata{
		Path:     file.Path,
		MTime:    info.ModTime().UnixMilli(),
		CTime:    info.ModTime().UnixMilli(),
		Size:     info.Size(),
		Children: children,
	}
	encryptedPath, err := crypto.EncryptPathMeta(meta, passphrase, pbkdf2Salt)
	if err != nil {
		return fmt.Errorf("encrypt path: %w", err)
	}

	entryDoc := map[string]interface{}{
		"_id":      docID,
		"type":     "newnote",
		"path":     encryptedPath,
		"children": children,
		"ctime":    meta.CTime,
		"mtime":    meta.MTime,
		"size":     meta.Size,
		"e_":       true,
	}
	entryJSON, _ := json.Marshal(entryDoc)
	rev := "1-" + shortHash(entryJSON)
	if err := store.UpsertDoc(docID, rev, entryJSON, false); err != nil {
		return fmt.Errorf("upsert entry %s: %w", docID, err)
	}

	h := sha256.Sum256(data)
	contentHash := hex.EncodeToString(h[:])
	if err := store.UpsertVaultFile(file.Path, docID, rev, contentHash,
		info.ModTime().UnixMilli(), info.Size()); err != nil {
		return fmt.Errorf("upsert vault_file: %w", err)
	}

	return nil
}

func pushDeleteToStore(store *localdb.Store, file ChangedFile, passphrase string) error {
	docID := hash.Path2ID(file.Path, passphrase)

	entryDoc := map[string]interface{}{
		"_id":      docID,
		"_deleted": true,
	}
	entryJSON, _ := json.Marshal(entryDoc)
	rev := "2-" + shortHash(entryJSON)
	if err := store.UpsertDoc(docID, rev, entryJSON, true); err != nil {
		return fmt.Errorf("upsert deleted entry %s: %w", docID, err)
	}

	store.DeleteVaultFile(file.Path)
	return nil
}

func shortHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8])
}
