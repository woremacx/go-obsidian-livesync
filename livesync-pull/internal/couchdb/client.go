package couchdb

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Client is a CouchDB HTTP client with Basic Auth.
type Client struct {
	baseURL  string
	db       string
	user     string
	password string
	http     *http.Client
}

// NewClient creates a new CouchDB client.
func NewClient(baseURL, db, user, password string) *Client {
	return &Client{
		baseURL:  baseURL,
		db:       db,
		user:     user,
		password: password,
		http:     &http.Client{},
	}
}

func (c *Client) doGet(path string) ([]byte, error) {
	u := fmt.Sprintf("%s/%s/%s", c.baseURL, c.db, path)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.password)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("CouchDB %s returned %d: %s", path, resp.StatusCode, string(body))
	}
	return body, nil
}

// GetSyncParams fetches the _local/obsidian_livesync_sync_parameters document.
func (c *Client) GetSyncParams() (*SyncParams, error) {
	body, err := c.doGet("_local/obsidian_livesync_sync_parameters")
	if err != nil {
		return nil, fmt.Errorf("fetch sync params: %w", err)
	}
	var params SyncParams
	if err := json.Unmarshal(body, &params); err != nil {
		return nil, fmt.Errorf("parse sync params: %w", err)
	}
	return &params, nil
}

// GetChanges fetches a batch of changes from CouchDB.
func (c *Client) GetChanges(since string, limit int) (*ChangesResponse, error) {
	params := url.Values{}
	params.Set("since", since)
	params.Set("include_docs", "true")
	params.Set("limit", fmt.Sprintf("%d", limit))
	path := "_changes?" + params.Encode()
	body, err := c.doGet(path)
	if err != nil {
		return nil, fmt.Errorf("fetch changes: %w", err)
	}
	var resp ChangesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse changes: %w", err)
	}
	return &resp, nil
}
