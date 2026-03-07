package localdb

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// Store is a SQLite-based local document store.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS documents (
			id TEXT PRIMARY KEY,
			rev TEXT NOT NULL,
			doc JSON NOT NULL,
			deleted INTEGER DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS vault_files (
			path TEXT PRIMARY KEY,
			doc_id TEXT NOT NULL,
			rev TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			mtime INTEGER NOT NULL,
			size INTEGER NOT NULL
		);
	`)
	return err
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// UpsertDoc inserts or replaces a document.
func (s *Store) UpsertDoc(id, rev string, doc json.RawMessage, deleted bool) error {
	del := 0
	if deleted {
		del = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO documents (id, rev, doc, deleted) VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET rev=excluded.rev, doc=excluded.doc, deleted=excluded.deleted`,
		id, rev, string(doc), del,
	)
	return err
}

// GetDoc retrieves a single document by ID.
func (s *Store) GetDoc(id string) (json.RawMessage, error) {
	var doc string
	err := s.db.QueryRow("SELECT doc FROM documents WHERE id = ?", id).Scan(&doc)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(doc), nil
}

// DocRow holds a document ID and its raw JSON.
type DocRow struct {
	ID      string
	Rev     string
	Doc     json.RawMessage
	Deleted bool
}

// GetAllDocs returns all non-deleted documents.
func (s *Store) GetAllDocs() ([]DocRow, error) {
	rows, err := s.db.Query("SELECT id, rev, doc, deleted FROM documents")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []DocRow
	for rows.Next() {
		var r DocRow
		var del int
		var doc string
		if err := rows.Scan(&r.ID, &r.Rev, &doc, &del); err != nil {
			return nil, err
		}
		r.Doc = json.RawMessage(doc)
		r.Deleted = del != 0
		result = append(result, r)
	}
	return result, rows.Err()
}

// GetMeta retrieves a metadata value by key.
func (s *Store) GetMeta(key string) (string, error) {
	var val string
	err := s.db.QueryRow("SELECT value FROM metadata WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

// SetMeta sets a metadata key-value pair.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO metadata (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	return err
}

// GetLastSeq returns the last replicated sequence, or "0" if none.
func (s *Store) GetLastSeq() (string, error) {
	seq, err := s.GetMeta("last_seq")
	if err != nil {
		return "0", err
	}
	if seq == "" {
		return "0", nil
	}
	return seq, nil
}

// SetLastSeq saves the last replicated sequence.
func (s *Store) SetLastSeq(seq string) error {
	return s.SetMeta("last_seq", seq)
}

// CountDocs returns the total number of documents.
func (s *Store) CountDocs() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM documents").Scan(&count)
	return count, err
}

// CountNonDeletedDocs returns the number of non-deleted documents.
func (s *Store) CountNonDeletedDocs() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM documents WHERE deleted = 0").Scan(&count)
	return count, err
}

// VaultFileRecord represents a tracked file in the vault.
type VaultFileRecord struct {
	Path        string
	DocID       string
	Rev         string
	ContentHash string
	MTime       int64
	Size        int64
}

// GetVaultFiles returns all vault file records as a map keyed by path.
func (s *Store) GetVaultFiles() (map[string]VaultFileRecord, error) {
	rows, err := s.db.Query("SELECT path, doc_id, rev, content_hash, mtime, size FROM vault_files")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]VaultFileRecord)
	for rows.Next() {
		var r VaultFileRecord
		if err := rows.Scan(&r.Path, &r.DocID, &r.Rev, &r.ContentHash, &r.MTime, &r.Size); err != nil {
			return nil, err
		}
		result[r.Path] = r
	}
	return result, rows.Err()
}

// UpsertVaultFile inserts or updates a vault file record.
func (s *Store) UpsertVaultFile(path, docID, rev, contentHash string, mtime, size int64) error {
	_, err := s.db.Exec(
		`INSERT INTO vault_files (path, doc_id, rev, content_hash, mtime, size) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET doc_id=excluded.doc_id, rev=excluded.rev, content_hash=excluded.content_hash, mtime=excluded.mtime, size=excluded.size`,
		path, docID, rev, contentHash, mtime, size,
	)
	return err
}

// DeleteVaultFile removes a vault file record.
func (s *Store) DeleteVaultFile(path string) error {
	_, err := s.db.Exec("DELETE FROM vault_files WHERE path = ?", path)
	return err
}

// BeginTx starts a transaction and returns a TxStore for batch operations.
func (s *Store) BeginTx() (*TxStore, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	return &TxStore{tx: tx}, nil
}

// TxStore wraps a transaction for batch operations.
type TxStore struct {
	tx   *sql.Tx
	stmt *sql.Stmt
}

// Prepare prepares the upsert statement for batch use.
func (t *TxStore) Prepare() error {
	var err error
	t.stmt, err = t.tx.Prepare(
		`INSERT INTO documents (id, rev, doc, deleted) VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET rev=excluded.rev, doc=excluded.doc, deleted=excluded.deleted`,
	)
	return err
}

// UpsertDoc inserts or replaces a document within the transaction.
func (t *TxStore) UpsertDoc(id, rev string, doc json.RawMessage, deleted bool) error {
	if t.stmt == nil {
		return fmt.Errorf("transaction not prepared; call Prepare() first")
	}
	del := 0
	if deleted {
		del = 1
	}
	_, err := t.stmt.Exec(id, rev, string(doc), del)
	return err
}

// Commit commits the transaction.
func (t *TxStore) Commit() error {
	if t.stmt != nil {
		t.stmt.Close()
	}
	return t.tx.Commit()
}

// Rollback rolls back the transaction.
func (t *TxStore) Rollback() error {
	if t.stmt != nil {
		t.stmt.Close()
	}
	return t.tx.Rollback()
}
