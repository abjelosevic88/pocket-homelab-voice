package store

import (
	"encoding/json"
	"strings"
)

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Memo statuses.
const (
	StatusQueued  = "queued"  // waiting to be written out
	StatusWorking = "working" // being written out now
	StatusDone    = "done"    // the words are there
	StatusFailed  = "failed"  // it could not be written out (the recording is kept)
)

// Kinds of memo: what it is about, guessed by the AI or set by the person.
var Kinds = []string{"note", "todo", "idea", "journal", "meeting", "shopping", "reminder"}

// ValidKind accepts "" or one of Kinds.
func ValidKind(k string) bool {
	if k == "" {
		return true
	}
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

// Segment is one stretch of speech, in milliseconds.
type Segment struct {
	Start int64  `json:"s"`
	End   int64  `json:"e"`
	Text  string `json:"t"`
}

// Action is something the AI found in a memo that belongs in another app:
// a task, an appointment, a line for today's note, money spent.
type Action struct {
	Type     string  `json:"type"` // task | event | journal | expense
	Title    string  `json:"title"`
	Notes    string  `json:"notes,omitempty"`
	Due      string  `json:"due,omitempty"`      // task: YYYY-MM-DD
	Start    string  `json:"start,omitempty"`    // event: YYYY-MM-DD or YYYY-MM-DDTHH:MM
	End      string  `json:"end,omitempty"`      // event
	Location string  `json:"location,omitempty"` // event
	Text     string  `json:"text,omitempty"`     // journal line
	Amount   float64 `json:"amount,omitempty"`   // expense, in major units
	Currency string  `json:"currency,omitempty"` // expense
	Date     string  `json:"date,omitempty"`     // expense: YYYY-MM-DD

	// Filled once it went there.
	SentTo  string `json:"sent_to,omitempty"` // app slug
	SentRef string `json:"sent_ref,omitempty"`
	SentURL string `json:"sent_url,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
}

// Memo is one recording: the audio, its words and where it went.
type Memo struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"-"`
	Title       string    `json:"title"`
	TitleSet    bool      `json:"title_set"` // named by the person: the AI leaves it
	RecordedAt  string    `json:"recorded_at"`
	DurationMS  int64     `json:"duration_ms"`
	Audio       string    `json:"-"`
	AudioType   string    `json:"audio_type"`
	AudioBytes  int64     `json:"audio_bytes"`
	HasAudio    bool      `json:"has_audio"`
	Peaks       []int     `json:"peaks"`
	Language    string    `json:"language"` // asked for, then what was heard
	Status      string    `json:"status"`
	Error       string    `json:"error"`
	Transcript  string    `json:"transcript"`
	Segments    []Segment `json:"segments"`
	Edited      bool      `json:"edited"`
	Engine      string    `json:"engine"`
	Summary     string    `json:"summary"`
	Tidy        string    `json:"tidy"`
	Kind        string    `json:"kind"`
	KindSet     bool      `json:"kind_set"`
	Actions     []Action  `json:"actions"`
	AIStatus    string    `json:"ai_status"` // "" | working | done | failed
	AIError     string    `json:"ai_error"`
	AIProvider  string    `json:"ai_provider"`
	Tags        string    `json:"tags"`
	Starred     bool      `json:"starred"`
	Filed       bool      `json:"filed"`
	Source      string    `json:"source"` // "" recorded here | upload | import | <app slug>
	SourceRef   string    `json:"source_ref"`
	AudioGoneAt string    `json:"audio_gone_at"` // the recording was let go (Settings → keep recordings)
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`

	// Filled by listings.
	Sent []Sent `json:"sent"`
}

// Sent is a memo (or part of it) that went to another Pocket app.
type Sent struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"-"`
	MemoID    int64  `json:"memo_id"`
	App       string `json:"app"`  // notes | tasks | calendar | money
	Kind      string `json:"kind"` // note | daily | task | event | expense
	Ref       string `json:"ref"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
}

// Conn is how a person reaches one sibling Pocket app: "core" (an app link
// through Pocket Core), "direct" (URL + token) or "off"; "" = not chosen yet.
type Conn struct {
	Mode      string `json:"mode"`
	URL       string `json:"url"`
	Token     string `json:"token"`
	PublicURL string `json:"public_url"`
}

// Settings are the box-wide values plus one person's preferences and their
// own connections.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`
	// The whisper.cpp model this box writes with (admins choose; box-wide,
	// the models are big).
	SpeechModel string `json:"speech_model"`
	// The model dictation from other apps is written out with (POST /transcribe): speed matters there.
	DictationModel string `json:"dictation_model"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`
	WeekStart  int    `json:"week_start"` // 1 = Monday, 7 = Sunday
	Currency   string `json:"currency"`

	// Writing out.
	Engine      string `json:"engine"`     // local (on this server) | api (OpenAI or a speech server)
	Language    string `json:"language"`   // "" = find out each time
	Vocabulary  string `json:"vocabulary"` // names and words to expect
	Latin       bool   `json:"latin"`      // Serbian Cyrillic in Latin letters
	SpeechAPI   string `json:"speech_api_model"`
	KeepAudio   int    `json:"keep_audio_days"` // 0 = forever
	PlaybackSpd string `json:"playback_speed"`  // 1 | 1.25 | 1.5 | 2

	// What the AI does after each memo.
	AITidy     bool `json:"ai_tidy"`      // tidy up, title, summary, suggestions
	FileOnSend bool `json:"file_on_send"` // sending somewhere takes it out of the inbox

	// Where things go in the other apps.
	NotesFolder     string `json:"notes_folder"`
	NotesListen     bool   `json:"notes_listen"` // a "listen" link under a note
	TasksListID     int64  `json:"tasks_list_id"`
	CalendarID      int64  `json:"calendar_id"`
	MoneyAccountID  int64  `json:"money_account_id"`
	MoneyCategoryID int64  `json:"money_category_id"`

	// Notifications.
	NotifyDone   bool   `json:"notify_done"`   // a long memo or an import is written out
	NotifyFailed bool   `json:"notify_failed"` // a memo could not be written out
	NotifyInbox  bool   `json:"notify_inbox"`  // morning: memos waiting to be filed
	NotifyHour   int    `json:"notify_hour"`
	NotifyURL    string `json:"notify_url"`
	NotifyFormat string `json:"notify_format"` // apprise | ntfy | json

	// Each sibling Pocket app, by slug.
	Conns map[string]Conn `json:"conns"`

	// AI: Pocket AI through Core, or an OpenAI-compatible API with the
	// person's own key (that key also writes out when Engine is api).
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
}

// SplitTags turns "a, b ,c" into clean lowercase tags.
func SplitTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#")))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// JoinTags is the stored form of tags.
func JoinTags(s string) string { return strings.Join(SplitTags(s), ", ") }

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
