// Package pocket talks to sibling Pocket apps: Pocket Notes (a memo as a
// note, a line in today's note), Pocket Tasks (what you said you have to do),
// Pocket Calendar (an appointment you mentioned) and Pocket Money (money you
// said you spent). Same bearer-token JSON API everywhere.
package pocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a bearer-token JSON client for one Pocket app.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New creates a client.
func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: timeout}}
}

// Configured reports whether URL and token are set.
func (c *Client) Configured() bool { return c != nil && c.BaseURL != "" && c.Token != "" }

// ErrNotFound is a 404 from the sibling app.
var ErrNotFound = errors.New("not found")

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, v any) error {
	if !c.Configured() {
		return errors.New("not configured")
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errors.New("token rejected (401)")
	}
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2000))
		_ = json.Unmarshal(b, &e)
		if e.Error != "" {
			return fmt.Errorf("HTTP %d: %s", res.StatusCode, e.Error)
		}
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if v == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// Health is the connection test for any Pocket app: /healthz plus a token check.
func (c *Client) Health(ctx context.Context) (string, error) {
	var out struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if !c.Configured() {
		return "", errors.New("not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
	if err != nil {
		return "", err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	var me map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/v1/auth/me", nil, nil, &me); err != nil {
		return out.Version, err
	}
	return out.Version, nil
}

// Do is a raw JSON call (for a connection test).
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body, v any) error {
	return c.do(ctx, method, path, q, body, v)
}

// list decodes a list that may be bare or wrapped in {key:[…]} / {results:[…]}.
func list[T any](raw json.RawMessage, keys ...string) ([]T, error) {
	var out []T
	if err := json.Unmarshal(raw, &out); err == nil {
		return out, nil
	}
	var wrap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	for _, k := range append(keys, "results", "items") {
		if b, ok := wrap[k]; ok {
			if err := json.Unmarshal(b, &out); err != nil {
				return nil, err
			}
			return out, nil
		}
	}
	return []T{}, nil
}

// ---------------- Pocket Money ----------------

// Account is a Money account the person can see.
type Account struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
	Everyday bool   `json:"everyday"`
	Archived bool   `json:"archived"`
	Role     string `json:"role"` // owner | editor | viewer
}

// Accounts lists the accounts the person can book on.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var all []Account
	if err := c.do(ctx, http.MethodGet, "/api/v1/accounts", nil, nil, &all); err != nil {
		return nil, err
	}
	out := []Account{}
	for _, a := range all {
		if !a.Archived && a.Role != "viewer" {
			out = append(out, a)
		}
	}
	return out, nil
}

// Category is a Money category.
type Category struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Kind     string `json:"kind"`
	Archived bool   `json:"archived"`
}

// Categories lists the expense categories usable on an account.
func (c *Client) Categories(ctx context.Context, accountID int64) ([]Category, error) {
	var all []Category
	path := "/api/v1/categories"
	if accountID > 0 {
		path = fmt.Sprintf("/api/v1/accounts/%d/categories", accountID)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &all); err != nil {
		return nil, err
	}
	out := []Category{}
	for _, ct := range all {
		if !ct.Archived && ct.Kind != "income" {
			out = append(out, ct)
		}
	}
	return out, nil
}

// Tx is an expense someone mentioned in a memo, in the currency they said
// (Money converts to the account's).
type Tx struct {
	AccountID   int64
	Date        string
	Time        string
	Amount      int64 // minor units of Currency
	Currency    string
	Description string
	Payee       string
	CategoryID  int64
	Notes       string
	ExternalID  string
	Lat, Lng    *float64
}

// CreateTx books a new expense and returns its id.
func (c *Client) CreateTx(ctx context.Context, t Tx) (int64, error) {
	body := map[string]any{"kind": "expense", "account_id": t.AccountID, "date": t.Date, "amount": t.Amount, "currency": t.Currency,
		"description": t.Description, "payee": t.Payee, "tags": []string{}, "notes": t.Notes, "source": "voice", "external_id": t.ExternalID}
	if t.CategoryID > 0 {
		body["category_id"] = t.CategoryID
	}
	if t.Time != "" {
		body["time"] = t.Time
	}
	if t.Lat != nil && t.Lng != nil {
		body["lat"], body["lng"] = *t.Lat, *t.Lng
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/transactions", nil, body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// ---------------- Pocket Calendar ----------------

// Calendar is one of the person's calendars.
type Calendar struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Writable bool   `json:"writable"`
	Default  bool   `json:"is_default"`
}

// Calendars lists the calendars the person can write to.
func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/calendars", nil, nil, &raw); err != nil {
		return nil, err
	}
	all, err := list[Calendar](raw, "calendars")
	if err != nil {
		return nil, err
	}
	out := []Calendar{}
	for _, cal := range all {
		if cal.Writable {
			out = append(out, cal)
		}
	}
	return out, nil
}

// Event is an appointment someone mentioned: timed, or all day when no time was said.
type Event struct {
	CalendarID  int64
	Title       string
	Description string
	Location    string
	URL         string
	Start       string // YYYY-MM-DD or YYYY-MM-DDTHH:MM
	End         string // the same; "" = an hour after a timed start
}

func (e Event) body() map[string]any {
	b := map[string]any{"title": e.Title, "description": e.Description, "location": e.Location, "url": e.URL, "categories": []string{"Voice"}}
	if len(e.Start) <= 10 {
		b["all_day"], b["start"], b["busy"] = true, e.Start, false
		if len(e.End) == 10 && e.End > e.Start {
			b["end"] = e.End
		}
	} else {
		start, err := time.Parse("2006-01-02T15:04", e.Start[:16])
		if err != nil {
			b["all_day"], b["start"] = true, e.Start[:10]
		} else {
			end, err := time.Parse("2006-01-02T15:04", e.End)
			if err != nil || !end.After(start) {
				end = start.Add(time.Hour)
			}
			b["all_day"], b["start"], b["end"] = false, start.Format("2006-01-02T15:04"), end.Format("2006-01-02T15:04")
		}
	}
	if e.CalendarID > 0 {
		b["calendar_id"] = e.CalendarID
	}
	return b
}

// CreateEvent adds the event and returns its id.
func (c *Client) CreateEvent(ctx context.Context, e Event) (int64, error) {
	var out struct {
		EventID int64 `json:"event_id"`
		ID      any   `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/events", nil, e.body(), &out); err != nil {
		return 0, err
	}
	if out.EventID > 0 {
		return out.EventID, nil
	}
	// a bare {"id": 12} or an occurrence id like "12:20261104"
	var id int64
	_, _ = fmt.Sscan(strings.SplitN(fmt.Sprint(out.ID), ":", 2)[0], &id)
	return id, nil
}

// ---------------- Pocket Notes ----------------

// Note is a note in Pocket Notes.
type Note struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
}

// CreateNote adds a note in a folder ("" = the top).
func (c *Client) CreateNote(ctx context.Context, title, folder, content string) (*Note, error) {
	var out Note
	if err := c.do(ctx, http.MethodPost, "/api/v1/notes", nil, map[string]any{"title": title, "category": folder, "content": content}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Capture adds text to today's daily note (stamped with the time).
func (c *Client) Capture(ctx context.Context, text string) (*Note, error) {
	var out Note
	if err := c.do(ctx, http.MethodPost, "/api/v1/capture", nil, map[string]any{"text": text}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Folder is a folder in Pocket Notes.
type Folder struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// Folders lists the person's note folders.
func (c *Client) Folders(ctx context.Context) ([]Folder, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/folders", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Folder](raw, "folders")
}

// ---------------- Pocket Tasks ----------------

// Task is a task in Pocket Tasks.
type Task struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// CreateTask adds a task (due "" = no date) with a link back to the memo.
func (c *Client) CreateTask(ctx context.Context, title, notes, due, link string, listID int64) (*Task, error) {
	body := map[string]any{"title": title, "notes": notes, "url": link}
	if due != "" {
		body["due"] = due
	}
	if listID > 0 {
		body["list_id"] = listID
	}
	var out Task
	if err := c.do(ctx, http.MethodPost, "/api/v1/tasks", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TaskList is one of the person's task lists.
type TaskList struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// TaskLists lists the person's task lists.
func (c *Client) TaskLists(ctx context.Context) ([]TaskList, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/lists", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[TaskList](raw, "lists")
}
