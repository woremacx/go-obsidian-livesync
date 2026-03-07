package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/woremacx/go-obsidian-livesync/internal/couchdb"
	"github.com/woremacx/go-obsidian-livesync/internal/hash"
	"github.com/woremacx/go-obsidian-livesync/internal/localdb"
	"github.com/woremacx/go-obsidian-livesync/internal/logw"
	"github.com/woremacx/go-obsidian-livesync/internal/push"
)

func main() {
	urlFlag := flag.String("url", "", "CouchDB URL (e.g. https://couchdb.example.com)")
	userFlag := flag.String("user", "", "CouchDB username")
	passFlag := flag.String("pass", "", "CouchDB password")
	dbFlag := flag.String("db", "", "CouchDB database name")
	vaultFlag := flag.String("vault", "./vault", "Vault directory to push")
	dataFlag := flag.String("data", "", "SQLite database path (default: <db>.db)")
	forceFlag := flag.Bool("force", false, "Force content hash comparison for all files")
	verboseFlag := flag.String("v", "", "Log verbosity: debug or trace")
	dryRunFlag := flag.Bool("dry-run", false, "Detect changes without pushing")
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

	// Open SQLite store
	store, err := localdb.Open(*dataFlag)
	if err != nil {
		logw.Fatalf("open database: %v", err)
	}
	defer store.Close()

	// Create CouchDB client
	client := couchdb.NewClient(*urlFlag, *dbFlag, *userFlag, *passFlag)

	// Fetch sync parameters (pbkdf2 salt)
	pbkdf2Salt, err := fetchSyncParams(client, store)
	if err != nil {
		logw.Fatalf("sync params: %v", err)
	}

	// Pull first for safety: replicate latest state from CouchDB
	logw.Infof("Pulling latest changes from CouchDB...")
	_, err = replicate(client, store)
	if err != nil {
		logw.Fatalf("replicate: %v", err)
	}

	// Detect changes
	t0 := time.Now()
	changes, err := push.DetectChanges(store, *vaultFlag, *forceFlag)
	if err != nil {
		logw.Fatalf("detect changes: %v", err)
	}
	logw.Infof("Detected %d changes in %v", len(changes), time.Since(t0))

	if len(changes) == 0 {
		fmt.Println("Nothing to push")
		return
	}

	// Report changes
	creates, updates, deletes := 0, 0, 0
	for _, c := range changes {
		switch c.Action {
		case "create":
			creates++
		case "update":
			updates++
		case "delete":
			deletes++
		}
		logw.Debugf("  %s: %s", c.Action, c.Path)
	}
	fmt.Printf("Changes: %d new, %d modified, %d deleted\n", creates, updates, deletes)

	if *dryRunFlag {
		fmt.Println("Dry run, not pushing")
		return
	}

	// Compute hashed passphrase for chunk IDs
	hashedPassphrase := hash.ComputeHashedPassphrase(passphrase)

	// Push each change
	pushed, errors := 0, 0
	for _, c := range changes {
		if err := push.PushFile(client, store, c, *vaultFlag, passphrase, pbkdf2Salt, hashedPassphrase); err != nil {
			logw.Warnf("push %s: %v", c.Path, err)
			errors++
		} else {
			pushed++
		}
	}

	fmt.Printf("Done: %d pushed, %d errors\n", pushed, errors)
}

func replicate(client *couchdb.Client, store *localdb.Store) (int, error) {
	since, err := store.GetLastSeq()
	if err != nil {
		return 0, err
	}

	totalDocs := 0
	for {
		resp, err := client.GetChanges(since, 500)
		if err != nil {
			return 0, err
		}

		if len(resp.Results) == 0 {
			break
		}

		tx, err := store.BeginTx()
		if err != nil {
			return 0, err
		}
		if err := tx.Prepare(); err != nil {
			tx.Rollback()
			return 0, err
		}

		for _, result := range resp.Results {
			rev := ""
			if len(result.Changes) > 0 {
				rev = result.Changes[0].Rev
			}
			if err := tx.UpsertDoc(result.ID, rev, json.RawMessage(result.Doc), result.Deleted); err != nil {
				tx.Rollback()
				return 0, fmt.Errorf("upsert %s: %w", result.ID, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return 0, err
		}

		totalDocs += len(resp.Results)
		since = string(resp.LastSeq)

		if err := store.SetLastSeq(since); err != nil {
			return 0, err
		}

		logw.Debugf("Replicated %d docs (seq: %s)", totalDocs, truncateSeq(since))
	}

	if totalDocs > 0 {
		logw.Infof("Replicated %d docs from CouchDB", totalDocs)
	}
	return totalDocs, nil
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
