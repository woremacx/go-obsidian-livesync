package couchdb

import (
	"bytes"
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

func (c *Client) doRequest(method, path string, body interface{}) ([]byte, int, error) {
	u := fmt.Sprintf("%s/%s/%s", c.baseURL, c.db, path)
	var reqBody io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBytes)
	}
	req, err := http.NewRequest(method, u, reqBody)
	if err != nil {
		return nil, 0, err
	}
	req.SetBasicAuth(c.user, c.password)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return respBody, resp.StatusCode, nil
}

// GetDocRev returns the current revision of a document, or "" if not found.
func (c *Client) GetDocRev(id string) (string, error) {
	u := fmt.Sprintf("%s/%s/%s", c.baseURL, c.db, url.PathEscape(id))
	req, err := http.NewRequest("HEAD", u, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.user, c.password)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode == 404 {
		return "", nil
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HEAD %s returned %d", id, resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if len(etag) >= 2 && etag[0] == '"' && etag[len(etag)-1] == '"' {
		return etag[1 : len(etag)-1], nil
	}
	return etag, nil
}

// PutDoc creates or updates a document. The doc should include _id and optionally _rev.
func (c *Client) PutDoc(id string, doc interface{}) (*PutResponse, error) {
	body, status, err := c.doRequest("PUT", url.PathEscape(id), doc)
	if err != nil {
		return nil, err
	}
	if status != 201 && status != 200 {
		return nil, fmt.Errorf("PUT %s returned %d: %s", id, status, string(body))
	}
	var resp PutResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse put response: %w", err)
	}
	return &resp, nil
}

// BulkDocs creates or updates multiple documents at once.
func (c *Client) BulkDocs(docs []interface{}) ([]BulkDocResult, error) {
	payload := map[string]interface{}{
		"docs": docs,
	}
	body, status, err := c.doRequest("POST", "_bulk_docs", payload)
	if err != nil {
		return nil, err
	}
	if status != 201 && status != 200 {
		return nil, fmt.Errorf("_bulk_docs returned %d: %s", status, string(body))
	}
	var results []BulkDocResult
	if err := json.Unmarshal(body, &results); err != nil {
		return nil, fmt.Errorf("parse bulk_docs response: %w", err)
	}
	return results, nil
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

// GetChangesLongPoll uses feed=longpoll to wait for changes from CouchDB.
// It blocks until at least one change is available or the heartbeat interval passes.
// heartbeatMs is the heartbeat interval in milliseconds (e.g. 30000).
func (c *Client) GetChangesLongPoll(since string, heartbeatMs int) (*ChangesResponse, error) {
	params := url.Values{}
	params.Set("since", since)
	params.Set("include_docs", "true")
	params.Set("feed", "longpoll")
	params.Set("heartbeat", fmt.Sprintf("%d", heartbeatMs))
	u := fmt.Sprintf("%s/%s/_changes?%s", c.baseURL, c.db, params.Encode())

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.password)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("longpoll changes: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read longpoll response: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("longpoll _changes returned %d: %s", resp.StatusCode, string(body))
	}

	var result ChangesResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse longpoll changes: %w", err)
	}
	return &result, nil
}
