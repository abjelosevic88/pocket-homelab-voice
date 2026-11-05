package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Voice as
// the target: another Pocket app the person allowed in Core may act here for
// them with a pcx_ token, only within these scopes. Every route not listed
// in linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "voice:read", Name: "See your voice memos", Description: "Read your voice memos: their words, the tidy version and summary, what they are about, where you sent them and the recording itself — for your journal, dashboards and Pocket AI."},
	{Scope: "voice:write", Name: "Add voice memos", Description: "Add a recording or a dictated text as a memo to be written out and filed (\"keep this as a memo\"). It may change or remove only the memos that app added itself."},
	{Scope: "voice:transcribe", Name: "Write out speech on this server", Description: "Send a short recording and get its words back, written out by whisper on this server with your language and words settings, so dictation in other apps never leaves the box. It may also have dictated text tidied up (filler words out, punctuation) by your AI. Nothing is kept."},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(memos|summary|search|tags|kinds|day/\d{4}-\d{2}-\d{2})$`), "voice:read"},
	{"GET", regexp.MustCompile(`^/api/v1/memos/\d+(/audio)?$`), "voice:read"},
	{"POST", regexp.MustCompile(`^/api/v1/memos$`), "voice:write"},
	{"PUT DELETE", regexp.MustCompile(`^/api/v1/memos/\d+$`), "voice:write"},
	{"GET", regexp.MustCompile(`^/api/v1/speech$`), "voice:transcribe"},
	{"POST", regexp.MustCompile(`^/api/v1/(transcribe|polish)$`), "voice:transcribe"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.Audience != "voice" || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
