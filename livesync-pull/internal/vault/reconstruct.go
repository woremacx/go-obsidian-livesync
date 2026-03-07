package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/internal/crypto"
	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/internal/localdb"
	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/internal/types"
	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/logw"
)

// Stats tracks materialization results.
type Stats struct {
	Written   int
	Deleted   int
	Skipped   int
	Errors    int
	Unchanged int
}

// Materialize reads all documents from the store and writes files to vaultPath.
// If fullRebuild is false, unchanged files (same rev) are skipped.
func Materialize(store *localdb.Store, vaultPath, passphrase string, pbkdf2Salt []byte, dynamicIter, fullRebuild bool) (*Stats, error) {
	docs, err := store.GetAllDocs()
	if err != nil {
		return nil, fmt.Errorf("get all docs: %w", err)
	}

	logw.Infof("[materialize] total docs from SQLite: %d", len(docs))

	var vaultFiles map[string]localdb.VaultFileRecord
	if !fullRebuild {
		vaultFiles, err = store.GetVaultFiles()
		if err != nil {
			return nil, fmt.Errorf("get vault files: %w", err)
		}
		logw.Infof("[materialize] tracked vault files: %d", len(vaultFiles))
	}

	stats := &Stats{}

	for _, row := range docs {
		var doc types.EntryDoc
		if err := json.Unmarshal(row.Doc, &doc); err != nil {
			logw.Debugf("id=%s: JSON unmarshal failed: %v (raw first 200 chars: %s)", row.ID, err, truncStr(string(row.Doc), 200))
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

		logw.Tracef("id=%s type=%s path_len=%d path_prefix=%q children=%d encrypted=%v",
			row.ID, doc.Type, len(doc.Path), truncStr(doc.Path, 40), len(doc.Children), doc.Encrypted)

		// Resolve path
		filePath, meta, err := resolvePath(doc, passphrase, pbkdf2Salt, dynamicIter)
		if err != nil {
			logw.Warnf("skip id=%s type=%s: path resolve failed: %v", row.ID, doc.Type, err)
			logw.Debugf("  path raw (first 100): %q", truncStr(doc.Path, 100))
			logw.Debugf("  path bytes (first 20): %x", []byte(doc.Path)[:min(20, len(doc.Path))])
			stats.Errors++
			continue
		}
		if filePath == "" {
			logw.Tracef("id=%s: resolvePath returned empty, skipping", row.ID)
			stats.Skipped++
			continue
		}

		// Override children from encrypted metadata if available
		if meta != nil && len(meta.Children) > 0 {
			logw.Tracef("id=%s: overriding children from metadata: %d -> %d", row.ID, len(doc.Children), len(meta.Children))
			doc.Children = meta.Children
		}

		// Remove leading / from path
		filePath = strings.TrimPrefix(filePath, "/")

		// Handle deleted documents
		if row.Deleted || doc.Deleted || doc.CouchDeleted {
			fullPath := filepath.Join(vaultPath, filePath)
			if _, err := os.Stat(fullPath); err == nil {
				if err := os.Remove(fullPath); err != nil {
					logw.Warnf("delete %s: %v", filePath, err)
					stats.Errors++
				} else {
					store.DeleteVaultFile(filePath)
					stats.Deleted++
				}
			}
			continue
		}

		// Skip unchanged files (rev matches)
		if vaultFiles != nil {
			if rec, ok := vaultFiles[filePath]; ok && rec.Rev == row.Rev {
				stats.Unchanged++
				continue
			}
		}

		// Reconstruct content
		content, chunkParts, err := reconstructContent(store, doc, passphrase, pbkdf2Salt, dynamicIter)
		if err != nil {
			logw.Warnf("skip %s (id=%s): content reconstruction failed: %v", filePath, row.ID, err)
			stats.Errors++
			continue
		}

		logw.Tracef("id=%s path=%s: content_len=%d isPlainText=%v chunks=%d", row.ID, filePath, len(content), IsPlainText(filePath), len(chunkParts))

		// Determine if binary and decode
		var fileData []byte
		if IsPlainText(filePath) {
			fileData = []byte(content)
		} else {
			// Binary files: each chunk is independently base64-encoded,
			// so we must decode each chunk separately then concatenate bytes.
			if len(chunkParts) > 1 {
				logw.Tracef("id=%s path=%s: per-chunk binary decode (%d chunks)", row.ID, filePath, len(chunkParts))
				var allBytes []byte
				for ci, part := range chunkParts {
					decoded, derr := DecodeBinary(part)
					if derr != nil {
						logw.Warnf("skip %s (id=%s): binary decode chunk[%d] failed: %v", filePath, row.ID, ci, derr)
						err = derr
						break
					}
					allBytes = append(allBytes, decoded...)
				}
				if err != nil {
					stats.Errors++
					continue
				}
				fileData = allBytes
			} else {
				logw.Tracef("id=%s path=%s: single-chunk binary decode", row.ID, filePath)
				fileData, err = DecodeBinary(content)
				if err != nil {
					logw.Warnf("skip %s (id=%s): binary decode failed: %v", filePath, row.ID, err)
					stats.Errors++
					continue
				}
			}
		}

		// Write file
		fullPath := filepath.Join(vaultPath, filePath)
		if err := writeFile(fullPath, fileData); err != nil {
			logw.Warnf("skip %s: write: %v", filePath, err)
			stats.Errors++
			continue
		}

		// Set mtime if available
		var fileMtime int64
		if meta != nil && meta.MTime > 0 {
			t := time.UnixMilli(meta.MTime)
			os.Chtimes(fullPath, t, t)
			fileMtime = meta.MTime
		} else if doc.MTime > 0 {
			t := time.UnixMilli(doc.MTime)
			os.Chtimes(fullPath, t, t)
			fileMtime = doc.MTime
		} else {
			fileMtime = time.Now().UnixMilli()
		}

		// Track in vault_files
		h := sha256.Sum256(fileData)
		contentHash := hex.EncodeToString(h[:])
		if err := store.UpsertVaultFile(filePath, row.ID, row.Rev, contentHash, fileMtime, int64(len(fileData))); err != nil {
			logw.Warnf("vault_files upsert %s: %v", filePath, err)
		}

		stats.Written++
	}

	return stats, nil
}

// resolvePath determines the file path from a document.
func resolvePath(doc types.EntryDoc, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (string, *types.PathMetadata, error) {
	path := doc.Path
	if path == "" {
		return "", nil, nil
	}

	logw.Tracef("[resolvePath] id=%s checking path: isEncryptedMeta=%v isObfV2=%v isObfV1=%v",
		doc.ID, crypto.IsEncryptedMeta(path), crypto.IsPathObfuscatedV2(path), crypto.IsPathObfuscatedV1(path))

	if crypto.IsEncryptedMeta(path) {
		logw.Tracef("[resolvePath] id=%s: /\\: encrypted metadata detected, encrypted_part_prefix=%q",
			doc.ID, truncStr(path[3:], 30))
		meta, err := crypto.DecryptPathMeta(path, passphrase, pbkdf2Salt)
		if err != nil {
			return "", nil, fmt.Errorf("decrypt /\\: metadata: %w", err)
		}
		logw.Tracef("[resolvePath] id=%s: decrypted metadata: path=%q children=%d", doc.ID, meta.Path, len(meta.Children))
		return meta.Path, meta, nil
	}

	if crypto.IsPathObfuscatedV2(path) {
		return "", nil, fmt.Errorf("V2 obfuscated path (%%/\\) without /\\: metadata: %s", truncStr(path, 30))
	}

	if crypto.IsPathObfuscatedV1(path) {
		logw.Tracef("[resolvePath] id=%s: V1 obfuscated path, len=%d, ivHex=%s, saltHex=%s",
			doc.ID, len(path), truncStr(path[1:33], 32), truncStr(path[33:65], 32))
		decrypted, err := crypto.DecryptObfuscatedPathV1(path, passphrase, dynamicIter)
		if err != nil {
			return "", nil, fmt.Errorf("V1 path decrypt (len=%d, prefix=%q): %w", len(path), truncStr(path, 40), err)
		}
		logw.Tracef("[resolvePath] id=%s: V1 path decrypted to: %q", doc.ID, decrypted)
		return decrypted, nil, nil
	}

	logw.Tracef("[resolvePath] id=%s: plain path=%q", doc.ID, truncStr(path, 60))
	return path, nil, nil
}

// reconstructContent assembles file content from chunks or data field.
// Returns the joined content string and the individual chunk parts (for per-chunk binary decode).
func reconstructContent(store *localdb.Store, doc types.EntryDoc, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (string, []string, error) {
	if doc.Type == types.TypeNotes {
		logw.Tracef("[content] id=%s: notes type, extracting data field", doc.ID)
		s, err := extractDataString(doc.Data)
		return s, nil, err
	}

	// plain/newnote: children array contains chunk IDs
	children := doc.Children
	if len(children) == 0 {
		logw.Tracef("[content] id=%s: no children, returning empty", doc.ID)
		return "", nil, nil
	}

	logw.Tracef("[content] id=%s: %d children to fetch", doc.ID, len(children))

	// Try to get eden for fallback
	var eden map[string]types.EdenEntry
	if len(doc.Eden) > 0 {
		eden = decryptEden(doc.Eden, passphrase, pbkdf2Salt, dynamicIter)
		if eden != nil {
			logw.Tracef("[content] id=%s: eden available with %d entries", doc.ID, len(eden))
		}
	}

	var parts []string
	for i, chunkID := range children {
		data, err := getChunkData(store, chunkID, eden, passphrase, pbkdf2Salt, dynamicIter)
		if err != nil {
			return "", nil, fmt.Errorf("chunk[%d] id=%s: %w", i, chunkID, err)
		}
		if i == 0 {
			logw.Tracef("[content] id=%s: chunk[0]=%s data_len=%d data_prefix=%q", doc.ID, chunkID, len(data), truncStr(data, 30))
		}
		parts = append(parts, data)
	}
	joined := strings.Join(parts, "")
	logw.Tracef("[content] id=%s: joined %d chunks, total_len=%d", doc.ID, len(parts), len(joined))
	return joined, parts, nil
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
	return "", fmt.Errorf("data field is neither string nor string array: first 100=%s", truncStr(string(raw), 100))
}

// getChunkData retrieves and optionally decrypts a chunk's data.
func getChunkData(store *localdb.Store, chunkID string, eden map[string]types.EdenEntry, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (string, error) {
	doc, err := store.GetDoc(chunkID)
	if err != nil {
		return "", fmt.Errorf("db lookup error: %w", err)
	}

	if doc != nil {
		data, err := decryptChunkDoc(doc, chunkID, passphrase, pbkdf2Salt, dynamicIter)
		if err != nil {
			return "", fmt.Errorf("decrypt: %w", err)
		}
		return data, nil
	}

	// Fallback to eden
	if eden != nil {
		if entry, ok := eden[chunkID]; ok {
			logw.Tracef("[chunk] %s: found in eden (data_len=%d)", chunkID, len(entry.Data))
			return entry.Data, nil
		}
	}

	return "", fmt.Errorf("chunk not found in DB or eden")
}

// decryptChunkDoc decrypts a chunk document's data field.
func decryptChunkDoc(raw json.RawMessage, chunkID string, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (string, error) {
	var chunk struct {
		Data      string `json:"data"`
		Encrypted bool   `json:"e_"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return "", fmt.Errorf("unmarshal chunk JSON: %w", err)
	}

	if !chunk.Encrypted {
		return chunk.Data, nil
	}

	logw.Tracef("[chunk] %s: encrypted, data_prefix=%q", chunkID, truncStr(chunk.Data, 20))
	decrypted, err := crypto.Decrypt(chunk.Data, passphrase, pbkdf2Salt, dynamicIter)
	if err != nil {
		return "", fmt.Errorf("decrypt (prefix=%q): %w", truncStr(chunk.Data, 10), err)
	}
	return decrypted, nil
}

// decryptEden decrypts the eden field (encrypted chunk fallback cache).
func decryptEden(edenRaw json.RawMessage, passphrase string, pbkdf2Salt []byte, dynamicIter bool) map[string]types.EdenEntry {
	var edenMap map[string]json.RawMessage
	if err := json.Unmarshal(edenRaw, &edenMap); err != nil {
		logw.Debugf("[eden] unmarshal failed: %v", err)
		return nil
	}

	logw.Tracef("[eden] keys: %v", edenKeys(edenMap))

	// Check for encrypted eden
	if encData, ok := edenMap["h:++encrypted-hkdf"]; ok {
		var entry types.EdenEntry
		if err := json.Unmarshal(encData, &entry); err == nil {
			logw.Tracef("[eden] decrypting h:++encrypted-hkdf, data_prefix=%q", truncStr(entry.Data, 20))
			decrypted, err := crypto.Decrypt(entry.Data, passphrase, pbkdf2Salt, dynamicIter)
			if err == nil {
				var result map[string]types.EdenEntry
				if err := json.Unmarshal([]byte(decrypted), &result); err == nil {
					return result
				}
				logw.Debugf("[eden] h:++encrypted-hkdf JSON parse failed: %v", err)
			} else {
				logw.Debugf("[eden] h:++encrypted-hkdf decrypt failed: %v", err)
			}
		}
	}

	if encData, ok := edenMap["h:++encrypted"]; ok {
		var entry types.EdenEntry
		if err := json.Unmarshal(encData, &entry); err == nil {
			logw.Tracef("[eden] decrypting h:++encrypted, data_prefix=%q", truncStr(entry.Data, 20))
			decrypted, err := crypto.Decrypt(entry.Data, passphrase, pbkdf2Salt, dynamicIter)
			if err == nil {
				var result map[string]types.EdenEntry
				if err := json.Unmarshal([]byte(decrypted), &result); err == nil {
					return result
				}
				logw.Debugf("[eden] h:++encrypted JSON parse failed: %v", err)
			} else {
				logw.Debugf("[eden] h:++encrypted decrypt failed: %v", err)
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

func edenKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// writeFile creates parent directories and writes data to file.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func truncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
