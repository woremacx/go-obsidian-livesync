package types

import "encoding/json"

// DocumentType represents the type field in a LiveSync document.
type DocumentType string

const (
	TypePlain   DocumentType = "plain"
	TypeNewNote DocumentType = "newnote"
	TypeNotes   DocumentType = "notes"
	TypeLeaf    DocumentType = "leaf"
)

// EntryDoc is a generic LiveSync document from CouchDB.
type EntryDoc struct {
	ID       string          `json:"_id"`
	Rev      string          `json:"_rev"`
	Type     DocumentType    `json:"type"`
	Path     string          `json:"path,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Children []string        `json:"children,omitempty"`
	CTime    int64           `json:"ctime,omitempty"`
	MTime    int64           `json:"mtime,omitempty"`
	Size     int64           `json:"size,omitempty"`
	CouchDeleted bool            `json:"_deleted,omitempty"`
	Deleted      bool            `json:"deleted,omitempty"`
	Eden         json.RawMessage `json:"eden,omitempty"`
	Encrypted    bool            `json:"e_,omitempty"`
}

// EdenEntry is a single entry in the eden fallback cache.
type EdenEntry struct {
	Data  string `json:"data"`
	Epoch int    `json:"epoch"`
}

// PathMetadata is the decrypted /\: metadata.
type PathMetadata struct {
	Path     string   `json:"path"`
	MTime    int64    `json:"mtime"`
	CTime    int64    `json:"ctime"`
	Size     int64    `json:"size"`
	Children []string `json:"children,omitempty"`
}
