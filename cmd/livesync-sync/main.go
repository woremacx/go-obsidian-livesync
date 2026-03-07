package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/woremacx/go-obsidian-livesync/internal/couchdb"
	"github.com/woremacx/go-obsidian-livesync/internal/hash"
	"github.com/woremacx/go-obsidian-livesync/internal/localdb"
	"github.com/woremacx/go-obsidian-livesync/internal/logw"
	"github.com/woremacx/go-obsidian-livesync/internal/push"
	"github.com/woremacx/go-obsidian-livesync/internal/vault"
)

func main() {
	urlFlag := flag.String("url", "", "CouchDB URL (e.g. https://couchdb.example.com)")
	userFlag := flag.String("user", "", "CouchDB username")
	passFlag := flag.String("pass", "", "CouchDB password")
	dbFlag := flag.String("db", "", "CouchDB database name")
	vaultFlag := flag.String("vault", "", "Vault directory to sync (default: <db>)")
	dataFlag := flag.String("data", "", "SQLite database path (default: .<db>.db)")
	dynamicIterFlag := flag.Bool("dynamic-iter", false, "Use dynamic iteration count for V1 encryption")
	verboseFlag := flag.String("v", "", "Log verbosity: debug or trace")
	flag.Parse()

	switch *verboseFlag {
	case "debug":
		logw.SetLevel(logw.LogWrapDebug)
	case "trace":
		logw.SetLevel(logw.LogWrapTrace)
	}

	passphrase := os.Getenv("LIVESYNC_PASSPHRASE")
	if passphrase == "" {
		logw.Fatalf("LIVESYNC_PASSPHRASE environment variable is required")
	}

	if *urlFlag == "" || *dbFlag == "" || *userFlag == "" || *passFlag == "" {
		logw.Fatalf("--url, --db, --user, and --pass flags are required")
	}

	if *dataFlag == "" {
		*dataFlag = "." + *dbFlag + ".db"
	}
	if *vaultFlag == "" {
		*vaultFlag = *dbFlag
	}

	store, err := localdb.Open(*dataFlag)
	if err != nil {
		logw.Fatalf("open database: %v", err)
	}
	defer store.Close()

	client := couchdb.NewClient(*urlFlag, *dbFlag, *userFlag, *passFlag)

	pbkdf2Salt, err := fetchSyncParams(client, store)
	if err != nil {
		logw.Fatalf("sync params: %v", err)
	}

	// Initial pull: catch up with CouchDB
	fmt.Println("Initial pull...")
	changedIDs, err := replicateBatch(client, store)
	if err != nil {
		logw.Fatalf("initial replicate: %v", err)
	}

	var mu sync.Mutex

	if len(changedIDs) > 0 {
		mu.Lock()
		materializeIDs(store, changedIDs, *vaultFlag, passphrase, pbkdf2Salt, *dynamicIterFlag)
		mu.Unlock()
	}

	// Initial push: push any local changes not yet in CouchDB
	fmt.Println("Initial push...")
	mu.Lock()
	pushAll(client, store, *vaultFlag, passphrase, pbkdf2Salt)
	mu.Unlock()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start pull goroutine
	go pullLoop(client, store, *vaultFlag, passphrase, pbkdf2Salt, *dynamicIterFlag, &mu, quit)

	// Start push goroutine
	go pushLoop(client, store, *vaultFlag, passphrase, pbkdf2Salt, &mu, quit)

	fmt.Println("Syncing... (Ctrl+C to stop)")
	<-quit
	fmt.Println("\nShutting down.")
}

// pullLoop watches CouchDB for changes via longpoll and materializes them.
func pullLoop(client *couchdb.Client, store *localdb.Store,
	vaultPath, passphrase string, pbkdf2Salt []byte, dynamicIter bool,
	mu *sync.Mutex, quit chan os.Signal) {

	for {
		select {
		case <-quit:
			return
		default:
		}

		since, _ := store.GetLastSeq()
		resp, err := client.GetChangesLongPoll(since, 30000)
		if err != nil {
			logw.Warnf("[pull] longpoll error: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if len(resp.Results) == 0 {
			continue
		}

		mu.Lock()

		// Save to SQLite
		var changedIDs []string
		tx, err := store.BeginTx()
		if err != nil {
			mu.Unlock()
			logw.Warnf("[pull] begin tx: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if err := tx.Prepare(); err != nil {
			tx.Rollback()
			mu.Unlock()
			logw.Warnf("[pull] prepare tx: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		txOK := true
		for _, result := range resp.Results {
			rev := ""
			if len(result.Changes) > 0 {
				rev = result.Changes[0].Rev
			}
			if err := tx.UpsertDoc(result.ID, rev, json.RawMessage(result.Doc), result.Deleted); err != nil {
				tx.Rollback()
				logw.Warnf("[pull] upsert %s: %v", result.ID, err)
				txOK = false
				break
			}
			changedIDs = append(changedIDs, result.ID)
		}

		if !txOK {
			mu.Unlock()
			time.Sleep(5 * time.Second)
			continue
		}

		if err := tx.Commit(); err != nil {
			mu.Unlock()
			logw.Warnf("[pull] commit: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if err := store.SetLastSeq(string(resp.LastSeq)); err != nil {
			logw.Warnf("[pull] set last seq: %v", err)
		}

		// Materialize changed docs (still under lock)
		materializeIDs(store, changedIDs, vaultPath, passphrase, pbkdf2Salt, dynamicIter)

		mu.Unlock()

		logw.Infof("[pull] %d changes processed", len(changedIDs))
	}
}

// pushLoop watches the vault directory for filesystem changes and pushes them.
func pushLoop(client *couchdb.Client, store *localdb.Store,
	vaultPath, passphrase string, pbkdf2Salt []byte,
	mu *sync.Mutex, quit chan os.Signal) {

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logw.Fatalf("[push] create watcher: %v", err)
	}
	defer watcher.Close()

	// Watch vault directory tree
	if err := addWatchRecursive(watcher, vaultPath); err != nil {
		logw.Fatalf("[push] watch vault: %v", err)
	}

	// Debounce: collect events, flush after 500ms of quiet
	debounceTimer := time.NewTimer(0)
	if !debounceTimer.Stop() {
		<-debounceTimer.C
	}
	pending := make(map[string]bool)

	for {
		select {
		case <-quit:
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Skip hidden files/dirs
			rel, err := filepath.Rel(vaultPath, event.Name)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if isHidden(rel) {
				continue
			}

			// Track new directories for watching
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					addWatchRecursive(watcher, event.Name)
					continue
				}
			}

			// Skip directory events
			if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
				continue
			}

			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) ||
				event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				pending[rel] = true
				debounceTimer.Reset(500 * time.Millisecond)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			logw.Warnf("[push] watcher error: %v", err)

		case <-debounceTimer.C:
			if len(pending) == 0 {
				continue
			}

			paths := make([]string, 0, len(pending))
			for p := range pending {
				paths = append(paths, p)
			}
			pending = make(map[string]bool)

			mu.Lock()
			pushChanged(client, store, vaultPath, passphrase, pbkdf2Salt, paths)
			mu.Unlock()
		}
	}
}

// pushChanged detects and pushes changes for the given paths.
func pushChanged(client *couchdb.Client, store *localdb.Store,
	vaultPath, passphrase string, pbkdf2Salt []byte, paths []string) {

	changes, err := push.DetectChanges(store, vaultPath)
	if err != nil {
		logw.Warnf("[push] detect changes: %v", err)
		return
	}

	if len(changes) == 0 {
		return
	}

	// Filter to only paths that triggered the event
	pathSet := make(map[string]bool, len(paths))
	for _, p := range paths {
		pathSet[p] = true
	}
	var filtered []push.ChangedFile
	for _, c := range changes {
		if pathSet[c.Path] {
			filtered = append(filtered, c)
		}
	}
	// Also include deletions detected by DetectChanges (file gone from disk)
	for _, c := range changes {
		if c.Action == "delete" && !pathSet[c.Path] {
			filtered = append(filtered, c)
		}
	}

	if len(filtered) == 0 {
		return
	}

	hashedPassphrase := hash.ComputeHashedPassphrase(passphrase)
	pushed, errors := 0, 0
	for _, c := range filtered {
		if err := push.PushFile(client, store, c, vaultPath, passphrase, pbkdf2Salt, hashedPassphrase); err != nil {
			logw.Warnf("[push] %s %s: %v", c.Action, c.Path, err)
			errors++
		} else {
			pushed++
		}
	}

	logw.Infof("[push] %d pushed, %d errors", pushed, errors)
}

// pushAll detects and pushes all local changes.
func pushAll(client *couchdb.Client, store *localdb.Store,
	vaultPath, passphrase string, pbkdf2Salt []byte) {

	changes, err := push.DetectChanges(store, vaultPath)
	if err != nil {
		logw.Warnf("[push] detect changes: %v", err)
		return
	}

	if len(changes) == 0 {
		fmt.Println("Nothing to push")
		return
	}

	hashedPassphrase := hash.ComputeHashedPassphrase(passphrase)
	pushed, errors := 0, 0
	for _, c := range changes {
		if err := push.PushFile(client, store, c, vaultPath, passphrase, pbkdf2Salt, hashedPassphrase); err != nil {
			logw.Warnf("[push] %s %s: %v", c.Action, c.Path, err)
			errors++
		} else {
			pushed++
		}
	}

	fmt.Printf("Initial push: %d pushed, %d errors\n", pushed, errors)
}

// materializeIDs materializes documents by their IDs and updates vault_files + last_materialized_seq.
func materializeIDs(store *localdb.Store, ids []string,
	vaultPath, passphrase string, pbkdf2Salt []byte, dynamicIter bool) {

	docs, err := store.GetDocsByIDs(ids)
	if err != nil {
		logw.Warnf("get docs by IDs: %v", err)
		return
	}

	stats, err := vault.Materialize(store, docs, vaultPath, passphrase, pbkdf2Salt, dynamicIter)
	if err != nil {
		logw.Warnf("materialize: %v", err)
		return
	}

	seq, _ := store.GetLastSeq()
	store.SetMeta("last_materialized_seq", seq)

	if stats.Written > 0 || stats.Deleted > 0 || stats.Errors > 0 {
		fmt.Printf("[pull] %d written, %d deleted, %d unchanged, %d errors\n",
			stats.Written, stats.Deleted, stats.Unchanged, stats.Errors)
	}
}

// replicateBatch pulls all pending changes from CouchDB in batches.
func replicateBatch(client *couchdb.Client, store *localdb.Store) ([]string, error) {
	since, err := store.GetLastSeq()
	if err != nil {
		return nil, err
	}

	totalDocs := 0
	var changedIDs []string
	for {
		resp, err := client.GetChanges(since, 500)
		if err != nil {
			return nil, err
		}

		if len(resp.Results) == 0 {
			break
		}

		tx, err := store.BeginTx()
		if err != nil {
			return nil, err
		}
		if err := tx.Prepare(); err != nil {
			tx.Rollback()
			return nil, err
		}

		for _, result := range resp.Results {
			rev := ""
			if len(result.Changes) > 0 {
				rev = result.Changes[0].Rev
			}
			if err := tx.UpsertDoc(result.ID, rev, json.RawMessage(result.Doc), result.Deleted); err != nil {
				tx.Rollback()
				return nil, fmt.Errorf("upsert %s: %w", result.ID, err)
			}
			changedIDs = append(changedIDs, result.ID)
		}

		if err := tx.Commit(); err != nil {
			return nil, err
		}

		totalDocs += len(resp.Results)
		since = string(resp.LastSeq)

		if err := store.SetLastSeq(since); err != nil {
			return nil, err
		}

		logw.Debugf("Replicated %d docs (seq: %s)", totalDocs, truncateSeq(since))
	}

	if totalDocs > 0 {
		fmt.Printf("Replicated %d docs from CouchDB\n", totalDocs)
	} else {
		fmt.Println("Already up to date")
	}
	return changedIDs, nil
}

func fetchSyncParams(client *couchdb.Client, store *localdb.Store) ([]byte, error) {
	cached, _ := store.GetMeta("pbkdf2salt")
	if cached != "" {
		salt, err := base64.StdEncoding.DecodeString(cached)
		if err == nil && len(salt) > 0 {
			params, err := client.GetSyncParams()
			if err == nil && params.PBKDF2Salt != "" && params.PBKDF2Salt != cached {
				store.SetMeta("pbkdf2salt", params.PBKDF2Salt)
				salt, _ = base64.StdEncoding.DecodeString(params.PBKDF2Salt)
			}
			return salt, nil
		}
	}

	params, err := client.GetSyncParams()
	if err != nil {
		return nil, fmt.Errorf("fetch sync params: %w", err)
	}

	if params.PBKDF2Salt == "" {
		return nil, fmt.Errorf("no pbkdf2salt in sync parameters")
	}

	store.SetMeta("pbkdf2salt", params.PBKDF2Salt)

	salt, err := base64.StdEncoding.DecodeString(params.PBKDF2Salt)
	if err != nil {
		return nil, fmt.Errorf("decode pbkdf2salt: %w", err)
	}
	return salt, nil
}

func truncateSeq(seq string) string {
	if len(seq) > 20 {
		return seq[:20] + "..."
	}
	return seq
}

// addWatchRecursive adds a directory and all its subdirectories to the watcher.
func addWatchRecursive(watcher *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, ".") && path != root {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

func isHidden(relPath string) bool {
	for _, part := range strings.Split(relPath, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}
