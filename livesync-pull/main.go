package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/internal/couchdb"
	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/internal/localdb"
	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/internal/vault"
)

func main() {
	urlFlag := flag.String("url", "", "CouchDB URL (e.g. https://couchdb.example.com)")
	userFlag := flag.String("user", "", "CouchDB username")
	passFlag := flag.String("pass", "", "CouchDB password")
	dbFlag := flag.String("db", "", "CouchDB database name")
	vaultFlag := flag.String("vault", "./vault", "Output vault directory")
	dataFlag := flag.String("data", ".livesync.db", "SQLite database path")
	dynamicIterFlag := flag.Bool("dynamic-iter", false, "Use dynamic iteration count for V1 encryption")
	flag.Parse()

	passphrase := os.Getenv("LIVESYNC_PASSPHRASE")
	if passphrase == "" {
		log.Fatal("LIVESYNC_PASSPHRASE environment variable is required")
	}

	if *urlFlag == "" || *dbFlag == "" || *userFlag == "" || *passFlag == "" {
		log.Fatal("--url, --db, --user, and --pass flags are required")
	}

	// Open SQLite store
	store, err := localdb.Open(*dataFlag)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer store.Close()

	// Create CouchDB client
	client := couchdb.NewClient(*urlFlag, *dbFlag, *userFlag, *passFlag)

	// Step 1: Replicate
	if err := replicate(client, store); err != nil {
		log.Fatalf("replicate: %v", err)
	}

	// Step 2: Fetch sync parameters
	pbkdf2Salt, err := fetchSyncParams(client, store)
	if err != nil {
		log.Fatalf("sync params: %v", err)
	}

	// Step 3: Materialize
	stats, err := vault.Materialize(store, *vaultFlag, passphrase, pbkdf2Salt, *dynamicIterFlag)
	if err != nil {
		log.Fatalf("materialize: %v", err)
	}

	fmt.Printf("Done: %d written, %d deleted, %d skipped, %d errors\n",
		stats.Written, stats.Deleted, stats.Skipped, stats.Errors)
}

func replicate(client *couchdb.Client, store *localdb.Store) error {
	since, err := store.GetLastSeq()
	if err != nil {
		return err
	}

	totalDocs := 0
	for {
		resp, err := client.GetChanges(since, 500)
		if err != nil {
			return err
		}

		if len(resp.Results) == 0 {
			break
		}

		tx, err := store.BeginTx()
		if err != nil {
			return err
		}
		if err := tx.Prepare(); err != nil {
			tx.Rollback()
			return err
		}

		for _, result := range resp.Results {
			rev := ""
			if len(result.Changes) > 0 {
				rev = result.Changes[0].Rev
			}
			if err := tx.UpsertDoc(result.ID, rev, json.RawMessage(result.Doc), result.Deleted); err != nil {
				tx.Rollback()
				return fmt.Errorf("upsert %s: %w", result.ID, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return err
		}

		totalDocs += len(resp.Results)
		since = string(resp.LastSeq)

		if err := store.SetLastSeq(since); err != nil {
			return err
		}

		fmt.Printf("Replicated %d docs (seq: %s)\n", totalDocs, truncateSeq(since))
	}

	if totalDocs > 0 {
		fmt.Printf("Replication complete: %d docs\n", totalDocs)
	} else {
		fmt.Println("Already up to date")
	}
	return nil
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
