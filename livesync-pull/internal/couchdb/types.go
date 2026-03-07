package couchdb

import (
	"encoding/json"
	"fmt"
)

// SeqValue handles CouchDB sequence values which can be string or number.
type SeqValue string

func (s *SeqValue) UnmarshalJSON(data []byte) error {
	// Try string first
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = SeqValue(str)
		return nil
	}
	// Try number
	var num json.Number
	if err := json.Unmarshal(data, &num); err == nil {
		*s = SeqValue(num.String())
		return nil
	}
	return fmt.Errorf("seq value is neither string nor number: %s", string(data))
}

// ChangesResponse is the JSON structure returned by CouchDB _changes endpoint.
type ChangesResponse struct {
	Results []ChangeResult `json:"results"`
	LastSeq SeqValue       `json:"last_seq"`
}

// ChangeResult is a single change entry.
type ChangeResult struct {
	Seq     SeqValue        `json:"seq"`
	ID      string          `json:"id"`
	Changes []ChangeRev     `json:"changes"`
	Doc     json.RawMessage `json:"doc,omitempty"`
	Deleted bool            `json:"deleted,omitempty"`
}

// ChangeRev is a revision reference in a change entry.
type ChangeRev struct {
	Rev string `json:"rev"`
}

// SyncParams is the _local/obsidian_livesync_sync_parameters document.
type SyncParams struct {
	ID         string `json:"_id"`
	Rev        string `json:"_rev"`
	PBKDF2Salt string `json:"pbkdf2salt"`
}
