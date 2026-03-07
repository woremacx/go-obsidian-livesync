package vault

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/internal/crypto"
	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/internal/localdb"
	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/internal/types"
)

// Stats tracks materialization results.
type Stats struct {
	Written int
	Deleted int
	Skipped int
	Errors  int
}

// Materialize reads all documents from the store and writes files to vaultPath.
func Materialize(store *localdb.Store, vaultPath, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (*Stats, error) {
	docs, err := store.GetAllDocs()
	if err != nil {
		return nil, fmt.Errorf("get all docs: %w", err)
	}

	stats := &Stats{}

	for _, row := range docs {
		var doc types.EntryDoc
		if err := json.Unmarshal(row.Doc, &doc); err != nil {
			stats.Errors++
			continue
		}

		// Skip non-file documents
		switch doc.Type {
		case types.TypePlain, types.TypeNewNote, types.TypeNotes:
			// These are file entries
		default:
			continue
		}

		// Resolve path
		filePath, meta, err := resolvePath(doc, passphrase, pbkdf2Salt, dynamicIter)
		if err != nil {
			log.Printf("WARN: skip %s: path resolve: %v", row.ID, err)
			stats.Errors++
			continue
		}
		if filePath == "" {
			stats.Skipped++
			continue
		}

		// Override children from encrypted metadata if available
		if meta != nil && len(meta.Children) > 0 {
			doc.Children = meta.Children
		}

		// Remove leading / from path
		filePath = strings.TrimPrefix(filePath, "/")

		// Handle deleted documents
		if row.Deleted || doc.Deleted || doc.CouchDeleted {
			fullPath := filepath.Join(vaultPath, filePath)
			if _, err := os.Stat(fullPath); err == nil {
				if err := os.Remove(fullPath); err != nil {
					log.Printf("WARN: delete %s: %v", filePath, err)
					stats.Errors++
				} else {
					stats.Deleted++
				}
			}
			continue
		}

		// Reconstruct content
		content, err := reconstructContent(store, doc, passphrase, pbkdf2Salt, dynamicIter)
		if err != nil {
			log.Printf("WARN: skip %s: content: %v", filePath, err)
			stats.Errors++
			continue
		}

		// Determine if binary and decode
		var fileData []byte
		if IsPlainText(filePath) {
			fileData = []byte(content)
		} else {
			fileData, err = DecodeBinary(content)
			if err != nil {
				log.Printf("WARN: skip %s: binary decode: %v", filePath, err)
				stats.Errors++
				continue
			}
		}

		// Write file
		fullPath := filepath.Join(vaultPath, filePath)
		if err := writeFile(fullPath, fileData); err != nil {
			log.Printf("WARN: skip %s: write: %v", filePath, err)
			stats.Errors++
			continue
		}

		// Set mtime if available
		if meta != nil && meta.MTime > 0 {
			t := time.UnixMilli(meta.MTime)
			os.Chtimes(fullPath, t, t)
		} else if doc.MTime > 0 {
			t := time.UnixMilli(doc.MTime)
			os.Chtimes(fullPath, t, t)
		}

		stats.Written++
	}

	return stats, nil
}

// resolvePath determines the file path from a document.
func resolvePath(doc types.EntryDoc, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (string, *types.PathMetadata, error) {
	path := doc.Path
	if path == "" {
		// Try to get path from _id (for docs without explicit path)
		return "", nil, nil
	}

	if crypto.IsEncryptedMeta(path) {
		meta, err := crypto.DecryptPathMeta(path, passphrase, pbkdf2Salt)
		if err != nil {
			return "", nil, err
		}
		return meta.Path, meta, nil
	}

	if crypto.IsPathObfuscatedV2(path) {
		// V2 obfuscation is one-way hash, cannot recover path without metadata
		// The path should have been stored as /\: metadata
		return "", nil, fmt.Errorf("V2 obfuscated path without metadata: %s", path[:min(len(path), 20)])
	}

	if crypto.IsPathObfuscatedV1(path) {
		decrypted, err := crypto.DecryptObfuscatedPathV1(path, passphrase, dynamicIter)
		if err != nil {
			return "", nil, err
		}
		return decrypted, nil, nil
	}

	return path, nil, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// reconstructContent assembles file content from chunks or data field.
func reconstructContent(store *localdb.Store, doc types.EntryDoc, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (string, error) {
	if doc.Type == types.TypeNotes {
		// notes type: data is string or string array, join directly
		return extractDataString(doc.Data)
	}

	// plain/newnote: children array contains chunk IDs
	children := doc.Children
	if len(children) == 0 {
		return "", nil
	}

	// Try to get eden for fallback
	var eden map[string]types.EdenEntry
	if len(doc.Eden) > 0 {
		eden = decryptEden(doc.Eden, passphrase, pbkdf2Salt, dynamicIter)
	}

	var parts []string
	for _, chunkID := range children {
		data, err := getChunkData(store, chunkID, eden, passphrase, pbkdf2Salt, dynamicIter)
		if err != nil {
			return "", fmt.Errorf("chunk %s: %w", chunkID, err)
		}
		parts = append(parts, data)
	}
	return strings.Join(parts, ""), nil
}

// extractDataString extracts the data field as a string (handles both string and string array JSON).
func extractDataString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	// Try string first
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	// Try string array
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return strings.Join(arr, ""), nil
	}
	return "", fmt.Errorf("data field is neither string nor string array")
}

// getChunkData retrieves and optionally decrypts a chunk's data.
func getChunkData(store *localdb.Store, chunkID string, eden map[string]types.EdenEntry, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (string, error) {
	doc, err := store.GetDoc(chunkID)
	if err != nil {
		return "", err
	}

	if doc != nil {
		return decryptChunkDoc(doc, passphrase, pbkdf2Salt, dynamicIter)
	}

	// Fallback to eden
	if eden != nil {
		if entry, ok := eden[chunkID]; ok {
			return entry.Data, nil
		}
	}

	return "", fmt.Errorf("chunk not found: %s", chunkID)
}

// decryptChunkDoc decrypts a chunk document's data field.
func decryptChunkDoc(raw json.RawMessage, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (string, error) {
	var chunk struct {
		Data      string `json:"data"`
		Encrypted bool   `json:"e_"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return "", err
	}

	if !chunk.Encrypted {
		return chunk.Data, nil
	}

	decrypted, err := crypto.Decrypt(chunk.Data, passphrase, pbkdf2Salt, dynamicIter)
	if err != nil {
		return "", fmt.Errorf("decrypt chunk: %w", err)
	}
	return decrypted, nil
}

// decryptEden decrypts the eden field (encrypted chunk fallback cache).
func decryptEden(edenRaw json.RawMessage, passphrase string, pbkdf2Salt []byte, dynamicIter bool) map[string]types.EdenEntry {
	var edenMap map[string]json.RawMessage
	if err := json.Unmarshal(edenRaw, &edenMap); err != nil {
		return nil
	}

	// Check for encrypted eden
	if encData, ok := edenMap["h:++encrypted-hkdf"]; ok {
		var entry types.EdenEntry
		if err := json.Unmarshal(encData, &entry); err == nil {
			decrypted, err := crypto.Decrypt(entry.Data, passphrase, pbkdf2Salt, dynamicIter)
			if err == nil {
				var result map[string]types.EdenEntry
				if err := json.Unmarshal([]byte(decrypted), &result); err == nil {
					return result
				}
			}
		}
	}

	if encData, ok := edenMap["h:++encrypted"]; ok {
		var entry types.EdenEntry
		if err := json.Unmarshal(encData, &entry); err == nil {
			decrypted, err := crypto.Decrypt(entry.Data, passphrase, pbkdf2Salt, dynamicIter)
			if err == nil {
				var result map[string]types.EdenEntry
				if err := json.Unmarshal([]byte(decrypted), &result); err == nil {
					return result
				}
			}
		}
	}

	// Non-encrypted eden
	var result map[string]types.EdenEntry
	if err := json.Unmarshal(edenRaw, &result); err == nil {
		return result
	}
	return nil
}

// writeFile creates parent directories and writes data to file.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
