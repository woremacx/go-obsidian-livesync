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
