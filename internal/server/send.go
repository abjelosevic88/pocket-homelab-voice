package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// ---- sending a memo where it belongs ----
//
// Every send goes to the person's own copy of the app, through the link they
// allowed in Pocket Core (or their own URL and token), with a link back to
// the memo. Voice remembers what went where, so the memo shows it and
// nothing is sent twice by accident.

type sendInput struct {
	To     string `json:"to"`     // note | daily | task | event | expense
	Action *int   `json:"action"` // the suggestion it comes from
	Title  string `json:"title"`
	Text   string `json:"text"`
	Folder string `json:"folder"`
	// task
	Due    string `json:"due"`
	Notes  string `json:"notes"`
	ListID int64  `json:"list_id"`
	// event
	Start      string `json:"start"`
	End        string `json:"end"`
	Location   string `json:"location"`
	CalendarID int64  `json:"calendar_id"`
	// expense
	Amount     float64 `json:"amount"`
	Currency   string  `json:"currency"`
	Date       string  `json:"date"`
	AccountID  int64   `json:"account_id"`
	CategoryID int64   `json:"category_id"`
}

var sendTargets = map[string]struct {
	app   pocketApp
	scope string
}{
	"note":    {appNotes, scopeNotesWrite},
	"daily":   {appNotes, scopeNotesWrite},
	"task":    {appTasks, scopeTasksWrite},
	"event":   {appCalendar, scopeCalendarWrite},
	"expense": {appMoney, scopeTxWrite},
}

func appURL(public, path string) string {
	if public == "" {
		return ""
	}
	return strings.TrimRight(public, "/") + path
}

// handleSend sends a memo, or one suggestion from it, to another app.
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	uid := actorID(ctx)
	m, err := s.store.GetMemo(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	var in sendInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	target, ok := sendTargets[in.To]
	if !ok {
		writeErr(w, http.StatusBadRequest, "send to note, daily, task, event or expense")
		return
	}
	if in.Action != nil && (*in.Action < 0 || *in.Action >= len(m.Actions)) {
		writeErr(w, http.StatusBadRequest, "no such suggestion")
		return
	}
	if in.Action != nil && m.Actions[*in.Action].SentTo != "" {
		writeErr(w, http.StatusConflict, "this suggestion was sent already")
		return
	}
	if in.Action != nil {
		fromAction(&in, m.Actions[*in.Action])
	}
	cl := s.clients(ctx, uid)
	conn, err := cl.conn(ctx, target.app, target.scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.sendMu.Lock()
	sent, err := s.sendOne(ctx, cl, conn, m, in)
	s.sendMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusBadGateway, target.app.name+": "+err.Error())
		return
	}
	sent.MemoID = m.ID
	if err := s.store.AddSent(ctx, uid, sent); err != nil {
		s.fail(w, err, "remember what was sent")
		return
	}
	m, err = s.store.GetMemo(ctx, uid, m.ID)
	if err != nil {
		s.fail(w, err, "memo")
		return
	}
	if in.Action != nil {
		a := &m.Actions[*in.Action]
		a.SentTo, a.SentRef, a.SentURL, a.Skipped = sent.App, sent.Ref, sent.URL, false
	}
	if cl.st.FileOnSend {
		m.Filed = true
	}
	if err := s.store.SaveMemo(ctx, uid, m); err != nil {
		s.fail(w, err, "memo")
		return
	}
	s.publish(ctx, uid, "voice.memo_filed", fmt.Sprintf("memo:%d:sent:%d", m.ID, sent.ID), map[string]any{
		"memo_id": m.ID, "title": m.Title, "app": sent.App, "kind": sent.Kind, "ref": sent.Ref, "item_title": sent.Title, "item_url": sent.URL, "url": s.memoURL(m.ID)})
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "memo": s.memoOut(ctx, m)})
}

// fromAction fills what the caller left out from the suggestion it sends
// (Pocket AI may send just {to, action}).
func fromAction(in *sendInput, a store.Action) {
	def := func(dst *string, v string) {
		if strings.TrimSpace(*dst) == "" {
			*dst = v
		}
	}
	if a.Type == "journal" {
		def(&in.Text, a.Text)
	} else {
		def(&in.Title, a.Title)
	}
	def(&in.Due, a.Due)
	def(&in.Notes, a.Notes)
	def(&in.Start, a.Start)
	def(&in.End, a.End)
	def(&in.Location, a.Location)
	def(&in.Currency, a.Currency)
	def(&in.Date, a.Date)
	if in.Amount == 0 {
		in.Amount = a.Amount
	}
}

// memoText is the words worth sending: the tidy version when there is one.
func memoText(m *store.Memo) string {
	return strings.TrimSpace(firstNonEmpty(m.Tidy, m.Transcript))
}

func (s *Server) sendOne(ctx context.Context, cl *clients, conn *siblingConn, m *store.Memo, in sendInput) (*store.Sent, error) {
	st := cl.st
	link := s.memoURL(m.ID)
	when := ""
	if t, err := time.Parse(time.RFC3339Nano, m.RecordedAt); err == nil {
		when = t.Local().Format("2 Jan 2006, 15:04")
	}
	title := cleanLine(in.Title, 160)
	switch in.To {
	case "note":
		text := strings.TrimSpace(in.Text)
		if text == "" {
			text = memoText(m)
		}
		if text == "" {
			return nil, fmt.Errorf("there are no words to save yet")
		}
		if title == "" {
			title = firstNonEmpty(m.Title, "Voice memo")
		}
		folder := strings.Trim(strings.TrimSpace(firstNonEmpty(in.Folder, st.NotesFolder)), "/")
		var b strings.Builder
		if m.Summary != "" && m.Summary != text {
			b.WriteString("> " + m.Summary + "\n\n")
		}
		b.WriteString(text)
		b.WriteString("\n\n---\n")
		b.WriteString("Voice memo · " + when + " · " + clock(m.DurationMS))
		if st.NotesListen && link != "" && m.HasAudio {
			b.WriteString(" · [Listen in Pocket Voice](" + link + ")")
		}
		b.WriteString("\n")
		n, err := conn.client.CreateNote(ctx, title, folder, b.String())
		if err != nil {
			return nil, err
		}
		return &store.Sent{App: "notes", Kind: "note", Ref: itoa(n.ID), Title: firstNonEmpty(n.Title, title), URL: appURL(conn.public, "/note/"+itoa(n.ID))}, nil
	case "daily":
		text := strings.TrimSpace(in.Text)
		if text == "" {
			text = firstNonEmpty(m.Summary, memoText(m))
		}
		if text == "" {
			return nil, fmt.Errorf("there are no words to add yet")
		}
		if link != "" {
			text += " ([memo](" + link + "))"
		}
		n, err := conn.client.Capture(ctx, text)
		if err != nil {
			return nil, err
		}
		return &store.Sent{App: "notes", Kind: "daily", Ref: itoa(n.ID), Title: firstNonEmpty(n.Title, "Today's note"), URL: appURL(conn.public, "/note/"+itoa(n.ID))}, nil
	case "task":
		if title == "" {
			return nil, fmt.Errorf("a task needs a title")
		}
		if !validDate(in.Due) {
			return nil, fmt.Errorf("the day is YYYY-MM-DD")
		}
		notes := strings.TrimSpace(in.Notes)
		if notes != "" {
			notes += "\n\n"
		}
		notes += "From a voice memo: " + firstNonEmpty(m.Title, when)
		listID := in.ListID
		if listID == 0 {
			listID = st.TasksListID
		}
		t, err := conn.client.CreateTask(ctx, title, notes, in.Due, link, listID)
		if err != nil {
			return nil, err
		}
		return &store.Sent{App: "tasks", Kind: "task", Ref: itoa(t.ID), Title: firstNonEmpty(t.Title, title), URL: appURL(conn.public, "/inbox?task="+itoa(t.ID))}, nil
	case "event":
		if title == "" {
			return nil, fmt.Errorf("an event needs a title")
		}
		start := strings.TrimSpace(in.Start)
		if !(len(start) == 10 && validDate(start)) && !(len(start) >= 16 && validDateTime(start[:16])) {
			return nil, fmt.Errorf("the start is YYYY-MM-DD or YYYY-MM-DDTHH:MM")
		}
		calID := in.CalendarID
		if calID == 0 {
			calID = st.CalendarID
		}
		desc := "From a voice memo (" + when + ")"
		if m.Summary != "" {
			desc = m.Summary + "\n\n" + desc
		}
		id, err := conn.client.CreateEvent(ctx, pocket.Event{CalendarID: calID, Title: title, Description: desc, Location: cleanLine(in.Location, 160), URL: link, Start: start, End: strings.TrimSpace(in.End)})
		if err != nil {
			return nil, err
		}
		return &store.Sent{App: "calendar", Kind: "event", Ref: itoa(id), Title: title, URL: appURL(conn.public, "/event/"+itoa(id))}, nil
	case "expense":
		if title == "" {
			return nil, fmt.Errorf("say what the money was for")
		}
		if in.Amount <= 0 || in.Amount > 1e7 || math.IsNaN(in.Amount) {
			return nil, fmt.Errorf("the amount is more than zero")
		}
		date := firstNonEmpty(in.Date, store.Today())
		if !validDate(date) {
			return nil, fmt.Errorf("the day is YYYY-MM-DD")
		}
		acct := in.AccountID
		if acct == 0 {
			acct = st.MoneyAccountID
		}
		cur := cleanCurrency(in.Currency)
		if acct == 0 || cur == "" {
			ac, err := cl.conn(ctx, appMoney, scopeAccountsRead)
			if err != nil {
				return nil, err
			}
			accts, err := ac.client.Accounts(ctx)
			if err != nil {
				return nil, err
			}
			pick := (*pocket.Account)(nil)
			for i := range accts {
				if (acct > 0 && accts[i].ID == acct) || (acct == 0 && accts[i].Everyday) {
					pick = &accts[i]
					break
				}
			}
			if pick == nil && acct == 0 && len(accts) > 0 {
				pick = &accts[0]
			}
			if pick == nil {
				return nil, fmt.Errorf("there is no account you can book on")
			}
			acct = pick.ID
			if cur == "" {
				cur = firstNonEmpty(cleanCurrency(st.Currency), pick.Currency)
			}
		}
		cat := in.CategoryID
		if cat == 0 {
			cat = st.MoneyCategoryID
		}
		n := len(m.Sent) + 1
		id, err := conn.client.CreateTx(ctx, pocket.Tx{AccountID: acct, Date: date, Amount: int64(math.Round(in.Amount * 100)), Currency: cur,
			Description: title, CategoryID: cat, Notes: "From a voice memo: " + link, ExternalID: fmt.Sprintf("voice:%d:%d", m.ID, n)})
		if err != nil {
			return nil, err
		}
		return &store.Sent{App: "money", Kind: "expense", Ref: itoa(id), Title: fmt.Sprintf("%s · %s %s", title, strconv.FormatFloat(in.Amount, 'f', 2, 64), cur), URL: appURL(conn.public, "/transactions/"+itoa(id))}, nil
	}
	return nil, fmt.Errorf("unknown target")
}
