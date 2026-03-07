package push

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/woremacx/go-obsidian-livesync/internal/couchdb"
	"github.com/woremacx/go-obsidian-livesync/internal/crypto"
	"github.com/woremacx/go-obsidian-livesync/internal/hash"
	"github.com/woremacx/go-obsidian-livesync/internal/localdb"
	"github.com/woremacx/go-obsidian-livesync/internal/logw"
	"github.com/woremacx/go-obsidian-livesync/internal/splitter"
	"github.com/woremacx/go-obsidian-livesync/internal/types"
)

// PushFile pushes a single file change to CouchDB.
func PushFile(client *couchdb.Client, store *localdb.Store, file ChangedFile,
	vaultPath, passphrase string, pbkdf2Salt []byte, hashedPassphrase string) error {

	if file.Action == "delete" {
		return pushDelete(client, store, file, passphrase)
	}

	fullPath := filepath.Join(vaultPath, file.Path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// Split content into chunks
	pieces := splitter.SplitContent(data, file.Path, splitter.DefaultPieceSize, splitter.DefaultMinChunkSize)
	if len(pieces) == 0 {
		pieces = []string{""}
	}

	// Create leaf docs for each chunk
	var children []string
	var leafDocs []interface{}
	for _, piece := range pieces {
		chunkID := hash.ComputeChunkID(piece, hashedPassphrase)
		children = append(children, chunkID)

		// Check if chunk already exists in CouchDB
		rev, err := client.GetDocRev(chunkID)
		if err != nil {
			logw.Warnf("check chunk %s: %v", chunkID, err)
		}
		if rev != "" {
			logw.Debugf("chunk %s already exists (rev=%s), skipping", chunkID, rev)
			continue
		}

		// Encrypt the chunk data
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
		leafDocs = append(leafDocs, leafDoc)
	}

	// Bulk upload leaf docs
	if len(leafDocs) > 0 {
		results, err := client.BulkDocs(leafDocs)
		if err != nil {
			return fmt.Errorf("bulk upload leaves: %w", err)
		}
		for _, r := range results {
			if r.Error != "" {
				logw.Warnf("leaf %s: %s", r.ID, r.Error)
			}
		}
	}

	// Generate document ID
	docID := hash.Path2ID(file.Path, passphrase)

	// Encrypt path metadata
	info, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	meta := &types.PathMetadata{
		Path:     file.Path,
		MTime:    info.ModTime().UnixMilli(),
		CTime:    info.ModTime().UnixMilli(), // Go can't easily get ctime
		Size:     info.Size(),
		Children: children,
	}
	encryptedPath, err := crypto.EncryptPathMeta(meta, passphrase, pbkdf2Salt)
	if err != nil {
		return fmt.Errorf("encrypt path: %w", err)
	}

	// Build entry doc
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

	// Check for existing revision
	existingRev, err := client.GetDocRev(docID)
	if err != nil {
		logw.Warnf("check existing doc %s: %v", docID, err)
	}
	if existingRev != "" {
		entryDoc["_rev"] = existingRev
	}

	// PUT entry doc
	putResp, err := client.PutDoc(docID, entryDoc)
	if err != nil {
		return fmt.Errorf("put entry doc: %w", err)
	}
	logw.Debugf("pushed %s -> %s (rev=%s)", file.Path, docID, putResp.Rev)

	// Update vault_files tracking
	h := sha256.Sum256(data)
	contentHash := hex.EncodeToString(h[:])
	if err := store.UpsertVaultFile(file.Path, docID, putResp.Rev, contentHash,
		info.ModTime().UnixMilli(), info.Size()); err != nil {
		logw.Warnf("update vault_files: %v", err)
	}

	return nil
}

// pushDelete removes a file from CouchDB.
func pushDelete(client *couchdb.Client, store *localdb.Store, file ChangedFile, passphrase string) error {
	docID := hash.Path2ID(file.Path, passphrase)

	rev, err := client.GetDocRev(docID)
	if err != nil {
		return fmt.Errorf("get rev for delete: %w", err)
	}
	if rev == "" {
		logw.Debugf("doc %s not found in CouchDB, skip delete", docID)
		store.DeleteVaultFile(file.Path)
		return nil
	}

	deleteDoc := map[string]interface{}{
		"_id":      docID,
		"_rev":     rev,
		"_deleted": true,
	}
	_, err = client.PutDoc(docID, deleteDoc)
	if err != nil {
		return fmt.Errorf("delete doc: %w", err)
	}

	store.DeleteVaultFile(file.Path)
	logw.Debugf("deleted %s -> %s", file.Path, docID)
	return nil
}
