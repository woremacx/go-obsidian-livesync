package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/woremacx/go-obsidian-livesync/internal/couchdb"
	"github.com/woremacx/go-obsidian-livesync/internal/localdb"
	"github.com/woremacx/go-obsidian-livesync/internal/logw"
	"github.com/woremacx/go-obsidian-livesync/internal/vault"
)

func main() {
	urlFlag := flag.String("url", "", "CouchDB URL (e.g. https://couchdb.example.com)")
	userFlag := flag.String("user", "", "CouchDB username")
	passFlag := flag.String("pass", "", "CouchDB password")
	dbFlag := flag.String("db", "", "CouchDB database name")
	vaultFlag := flag.String("vault", "", "Output vault directory (default: <db>)")
	dataFlag := flag.String("data", "", "SQLite database path (default: <db>.db)")
	dynamicIterFlag := flag.Bool("dynamic-iter", false, "Use dynamic iteration count for V1 encryption")
	fullFlag := flag.Bool("full", false, "Full rebuild: skip incremental change detection, rewrite all files")
	watchFlag := flag.Bool("watch", false, "Watch for changes continuously using CouchDB longpoll")
	verboseFlag := flag.String("v", "", "Log verbosity: debug or trace (default: info only)")
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

	// Open SQLite store
	store, err := localdb.Open(*dataFlag)
	if err != nil {
		logw.Fatalf("open database: %v", err)
	}
	defer store.Close()

	// Create CouchDB client
	client := couchdb.NewClient(*urlFlag, *dbFlag, *userFlag, *passFlag)

	// Step 1: Record pre-replication seq, then replicate
	preSeq, _ := store.GetLastSeq()
	changedIDs, err := replicate(client, store)
	if err != nil {
		logw.Fatalf("replicate: %v", err)
	}
	postSeq, _ := store.GetLastSeq()

	// Step 2: Fetch sync parameters
	pbkdf2Salt, err := fetchSyncParams(client, store)
	if err != nil {
		logw.Fatalf("sync params: %v", err)
	}

	// Step 3: Determine which docs to materialize
	materializedSeq, _ := store.GetMeta("last_materialized_seq")
	logw.Debugf("seq check: preSeq=%s postSeq=%s materializedSeq=%s changedIDs=%d",
		truncateSeq(preSeq), truncateSeq(postSeq), truncateSeq(materializedSeq), len(changedIDs))

	var docs []localdb.DocRow
	t0 := time.Now()
	switch {
	case materializedSeq == postSeq:
		// Already fully materialized
		fmt.Println("Nothing to materialize")
	case materializedSeq == preSeq && len(changedIDs) > 0:
		// Previous materialize completed, only process this run's changes
		docs, err = store.GetDocsByIDs(changedIDs)
		if err != nil {
			logw.Fatalf("get docs by IDs: %v", err)
		}
		logw.Infof("Incremental materialization: %d changed docs", len(docs))
	case *fullFlag:
		// Forced full rebuild
		docs, err = store.GetAllDocs()
		if err != nil {
			logw.Fatalf("get all docs: %v", err)
		}
		logw.Infof("Full rebuild: %d docs", len(docs))
	default:
		// Initial run, crash recovery, etc: only load docs that differ from vault_files
		docs, err = store.GetChangedDocs()
		if err != nil {
			logw.Fatalf("get changed docs: %v", err)
		}
		logw.Infof("Materialization (changed only): %d docs", len(docs))
	}

	logw.Debugf("doc selection took %v", time.Since(t0))

	if len(docs) > 0 {
		// Step 4: Materialize
		stats, err := vault.Materialize(store, docs, *vaultFlag, passphrase, pbkdf2Salt, *dynamicIterFlag)
		if err != nil {
			logw.Fatalf("materialize: %v", err)
		}

		// Step 5: Record successful materialization seq
		if err := store.SetMeta("last_materialized_seq", postSeq); err != nil {
			logw.Warnf("save last_materialized_seq: %v", err)
		}

		fmt.Printf("Done: %d written, %d unchanged, %d deleted, %d skipped, %d errors\n",
			stats.Written, stats.Unchanged, stats.Deleted, stats.Skipped, stats.Errors)
	}

	if !*watchFlag {
		return
	}

	// Watch mode: longpoll loop
	fmt.Println("Watching for changes...")
	for {
		since, _ := store.GetLastSeq()
		resp, err := client.GetChangesLongPoll(since, 30000)
		if err != nil {
			logw.Warnf("longpoll error: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if len(resp.Results) == 0 {
			continue
		}

		// Save changes to SQLite
		var changedIDs []string
		tx, err := store.BeginTx()
		if err != nil {
			logw.Warnf("begin tx: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if err := tx.Prepare(); err != nil {
			tx.Rollback()
			logw.Warnf("prepare tx: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, result := range resp.Results {
			rev := ""
			if len(result.Changes) > 0 {
				rev = result.Changes[0].Rev
			}
			if err := tx.UpsertDoc(result.ID, rev, json.RawMessage(result.Doc), result.Deleted); err != nil {
				tx.Rollback()
				logw.Warnf("upsert %s: %v (retrying in 5s)", result.ID, err)
				break
			}
			changedIDs = append(changedIDs, result.ID)
		}

		if err := tx.Commit(); err != nil {
			logw.Warnf("commit: %v (retrying in 5s)", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if err := store.SetLastSeq(string(resp.LastSeq)); err != nil {
			logw.Warnf("set last seq: %v", err)
		}

		logw.Infof("Received %d changes", len(changedIDs))

		// Materialize changed docs
		docs, err := store.GetDocsByIDs(changedIDs)
		if err != nil {
			logw.Warnf("get docs by IDs: %v", err)
			continue
		}

		wStats, err := vault.Materialize(store, docs, *vaultFlag, passphrase, pbkdf2Salt, *dynamicIterFlag)
		if err != nil {
			logw.Warnf("materialize: %v", err)
			continue
		}

		postSeq, _ := store.GetLastSeq()
		if err := store.SetMeta("last_materialized_seq", postSeq); err != nil {
			logw.Warnf("save last_materialized_seq: %v", err)
		}

		fmt.Printf("Watch: %d written, %d unchanged, %d deleted, %d skipped, %d errors\n",
			wStats.Written, wStats.Unchanged, wStats.Deleted, wStats.Skipped, wStats.Errors)
	}
}

func replicate(client *couchdb.Client, store *localdb.Store) ([]string, error) {
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

		fmt.Printf("Replicated %d docs (seq: %s)\n", totalDocs, truncateSeq(since))
	}

	if totalDocs > 0 {
		fmt.Printf("Replication complete: %d docs\n", totalDocs)
	} else {
		fmt.Println("Already up to date")
	}
	return changedIDs, nil
}

func fetchSyncParams(client *couchdb.Client, store *localdb.Store) ([]byte, error) {
	// Try cached salt first
	cached, _ := store.GetMeta("pbkdf2salt")
	if cached != "" {
		salt, err := base64.StdEncoding.DecodeString(cached)
		if err == nil && len(salt) > 0 {
			// Still fetch from server to update if needed
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
