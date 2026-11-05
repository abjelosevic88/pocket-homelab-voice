package server

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// ---------- pickers: what the other apps have ----------

// pick answers a picker: the items, or why the app is not reachable.
func pick(w http.ResponseWriter, items any, err error, app pocketApp) {
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": !isNotConnected(err), "detail": app.name + ": " + strings.TrimPrefix(err.Error(), app.name+" "), "items": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "items": items})
}

func (s *Server) handlePickMoneyAccounts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appMoney, scopeAccountsRead)
	if err != nil {
		pick(w, nil, err, appMoney)
		return
	}
	accts, err := conn.client.Accounts(ctx)
	pick(w, accts, err, appMoney)
}

func (s *Server) handlePickMoneyCategories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appMoney, scopeAccountsRead)
	if err != nil {
		pick(w, nil, err, appMoney)
		return
	}
	cats, err := conn.client.Categories(ctx, int64(queryInt(r, "account_id")))
	pick(w, cats, err, appMoney)
}

func (s *Server) handlePickCalendars(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appCalendar, scopeCalendarRead)
	if err != nil {
		pick(w, nil, err, appCalendar)
		return
	}
	cals, err := conn.client.Calendars(ctx)
	pick(w, cals, err, appCalendar)
}

func (s *Server) handlePickNoteFolders(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appNotes, scopeNotesRead)
	if err != nil {
		pick(w, nil, err, appNotes)
		return
	}
	fs, err := conn.client.Folders(ctx)
	pick(w, fs, err, appNotes)
}

func (s *Server) handlePickTaskLists(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appTasks, scopeTasksRead)
	if err != nil {
		pick(w, nil, err, appTasks)
		return
	}
	ls, err := conn.client.TaskLists(ctx)
	pick(w, ls, err, appTasks)
}
