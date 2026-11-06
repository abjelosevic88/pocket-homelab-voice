package server

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// demoMemo is one memo of the demo: what was said, the tidy version, and
// what the AI found in it. day is days ago, at hh:mm.
type demoMemo struct {
	day, hh, mm int
	lang, kind  string
	title, said string
	tidy, sum   string
	tags        string
	actions     []store.Action
	sent        []string // "app:kind:title" already sent
	star, filed bool
}

func demoDate(daysAgo int) string { return time.Now().AddDate(0, 0, -daysAgo).Format("2006-01-02") }

func demoNext(wd time.Weekday) string {
	t := time.Now()
	for i := 1; i <= 7; i++ {
		if d := t.AddDate(0, 0, i); d.Weekday() == wd {
			return d.Format("2006-01-02")
		}
	}
	return t.Format("2006-01-02")
}

func demoMemos() []demoMemo {
	today := demoDate(0)
	friday := demoNext(time.Friday)
	tuesday := demoNext(time.Tuesday)
	monthEnd := time.Now().AddDate(0, 1, -time.Now().Day()).Format("2006-01-02")
	return []demoMemo{
		{day: 0, hh: 8, mm: 12, lang: "en", kind: "todo", title: "Dentist, dry cleaning and photos for Amra",
			said: "Okay so today, uh, I need to call the dentist and move the appointment, it's on Thursday but I can't make it. Then pick up the dry cleaning before six. And, um, send Amra the photos from the weekend, she asked twice already.",
			tidy: "Today I need to call the dentist and move the appointment: it's on Thursday, but I can't make it. Then pick up the dry cleaning before six. And send Amra the photos from the weekend; she has asked twice already.",
			sum:  "Three things for today: move the dentist appointment, pick up the dry cleaning before six and send Amra the weekend photos.",
			actions: []store.Action{
				{Type: "task", Title: "Call the dentist to move Thursday's appointment", Due: today},
				{Type: "task", Title: "Pick up the dry cleaning", Due: today, Notes: "Before 18:00"},
				{Type: "task", Title: "Send Amra the photos from the weekend", Due: today},
			}},
		{day: 0, hh: 7, mm: 31, lang: "bs", kind: "journal", title: "Jutarnja šetnja do Vrela Bosne", tags: "šetnja",
			said: "Jutros sam ustao rano i prošetao do Vrela Bosne. Bilo je hladno, ali predivno, magla se dizala iznad vode i labudovi su već bili budni. Osjećam se odmorno, prvi put ove sedmice. Trebao bih ovo raditi češće, barem subotom.",
			tidy: "Jutros sam ustao rano i prošetao do Vrela Bosne. Bilo je hladno, ali predivno: magla se dizala iznad vode, a labudovi su već bili budni.\n\nOsjećam se odmorno, prvi put ove sedmice. Trebao bih ovo raditi češće, barem subotom.",
			sum:  "Rana šetnja do Vrela Bosne po magli; prvi put ove sedmice osjećaj odmora.",
			actions: []store.Action{
				{Type: "journal", Text: "Rano jutro na Vrelu Bosne, magla nad vodom i labudovi. Prvi put ove sedmice se osjećam odmorno."},
			}},
		{day: 1, hh: 18, mm: 34, lang: "en", kind: "shopping", title: "Groceries for the weekend", tags: "groceries",
			said: "Shopping list. Milk, eggs, the good bread from the bakery on the corner, tomatoes, cheese for the pita, and coffee, the Ethiopian one from Kafeterija, we're almost out.",
			tidy: "Shopping list:\n- Milk\n- Eggs\n- The good bread from the bakery on the corner\n- Tomatoes\n- Cheese for the pita\n- Coffee: the Ethiopian one from Kafeterija (almost out)",
			actions: []store.Action{
				{Type: "task", Title: "Shopping", Notes: "Milk, eggs, bread from the corner bakery, tomatoes, cheese for the pita, Ethiopian coffee from Kafeterija", Due: demoDate(-1)},
			}},
		{day: 1, hh: 13, mm: 5, lang: "en", kind: "journal", title: "Lunch with Mirza at Klopa", tags: "lunch",
			said: "Lunch with Mirza at Klopa, twenty four marks, I paid for both of us. He's thinking about leaving his job and going freelance. Told him to talk to Lejla first, she did the same thing last year.",
			tidy: "Lunch with Mirza at Klopa: 24 marks, I paid for both of us. He's thinking about leaving his job and going freelance. I told him to talk to Lejla first; she did the same last year.",
			sum:  "Lunch with Mirza (24 KM, I paid); he is thinking of going freelance and should talk to Lejla.",
			actions: []store.Action{
				{Type: "expense", Title: "Lunch with Mirza at Klopa", Amount: 24, Currency: "BAM", Date: demoDate(1)},
				{Type: "journal", Text: "Lunch with Mirza at Klopa; he's thinking about going freelance."},
			},
			sent: []string{"money:expense:Lunch with Mirza at Klopa · 24.00 BAM"}, filed: true},
		{day: 2, hh: 21, mm: 18, lang: "en", kind: "idea", title: "A weekly recap of memos and tasks", tags: "pocket, ideas", star: true,
			said: "Idea for the Pocket apps. A weekly email, or a push, that tells you what you recorded, what you finished and what's still open. It could use the inbox count from Voice and the tasks that came out of memos. Maybe Sunday evening, so you start the week clean.",
			tidy: "Idea for the Pocket apps: a weekly email or push that tells you what you recorded, what you finished and what is still open. It could use the inbox count from Voice and the tasks that came out of memos. Maybe on Sunday evening, so the week starts clean.",
			sum:  "A Sunday-evening recap of recorded memos, finished tasks and what is still open.",
			sent: []string{"notes:note:A weekly recap of memos and tasks"}, filed: true},
		{day: 3, hh: 16, mm: 2, lang: "en", kind: "meeting", title: "School: parent meeting and the Jahorina trip", tags: "school",
			said: "Notes from the call with the school. The parent meeting moved to " + time.Now().AddDate(0, 0, 9).Format("Monday the 2") + " at five in the evening, in the gym. We have to bring the signed form for the trip to Jahorina, and the deposit is fifty marks, by Friday.",
			tidy: "Notes from the call with the school:\n- The parent meeting moved to " + time.Now().AddDate(0, 0, 9).Format("Monday 2 January") + " at 17:00, in the gym.\n- Bring the signed form for the trip to Jahorina.\n- The deposit is 50 marks, due by Friday.",
			sum:  "The parent meeting moved; the Jahorina trip needs a signed form and a 50 KM deposit by Friday.",
			actions: []store.Action{
				{Type: "event", Title: "Parent meeting at school", Start: demoDate(-9) + "T17:00", End: demoDate(-9) + "T18:30", Location: "School gym"},
				{Type: "task", Title: "Sign the form for the Jahorina trip", Due: friday},
				{Type: "task", Title: "Pay the 50 KM deposit for the Jahorina trip", Due: friday},
			},
			sent: []string{"calendar:event:Parent meeting at school"}},
		{day: 4, hh: 9, mm: 40, lang: "en", kind: "reminder", title: "Renew the car registration",
			said: "Remind me to renew the car registration, it runs out at the end of the month. And the winter tyres, ask at the garage when I'm there anyway.",
			tidy: "Remind me to renew the car registration: it runs out at the end of the month. And ask about winter tyres at the garage while I'm there.",
			actions: []store.Action{
				{Type: "task", Title: "Renew the car registration", Due: monthEnd},
				{Type: "task", Title: "Ask the garage about winter tyres", Due: monthEnd},
			},
			sent: []string{"tasks:task:Renew the car registration", "tasks:task:Ask the garage about winter tyres"}, filed: true},
		{day: 5, hh: 22, mm: 47, lang: "en", kind: "journal", title: "The release went out", tags: "work",
			said: "Long day. The release went out in the end, after the database migration took forty minutes instead of five. Nobody noticed, which is the best outcome. Note to self, test migrations on a copy of the real data, not the demo data.",
			tidy: "Long day. The release went out in the end, after the database migration took forty minutes instead of five. Nobody noticed, which is the best outcome.\n\nNote to self: test migrations on a copy of the real data, not the demo data.",
			sum:  "The release shipped despite a 40-minute migration; next time test migrations on real data.",
			actions: []store.Action{
				{Type: "task", Title: "Test migrations on a copy of the real data", Notes: "Not the demo data"},
				{Type: "journal", Text: "The release went out after a forty-minute migration; nobody noticed."},
			}},
		{day: 6, hh: 19, mm: 10, lang: "bs", kind: "note", title: "Nanin recept za pitu", tags: "recepti", star: true,
			said: "Nanin recept za pitu, da ne zaboravim. Tijesto, kilo brašna, mlaka voda, malo soli i kašika ulja. Mijesi se dok ne bude mekano kao ušna resica. Odmara pola sata pod krpom, pa se razvlači na stolu dok ne vidiš kroz njega. Za sirnicu, sir i dva jaja i malo kajmaka.",
			tidy: "Nanin recept za pitu, da ne zaboravim.\n\nTijesto:\n- kilo brašna\n- mlaka voda\n- malo soli i kašika ulja\n\nMijesi se dok ne bude mekano kao ušna resica. Odmara pola sata pod krpom, pa se razvlači na stolu dok ne vidiš kroz njega.\n\nZa sirnicu: sir, dva jaja i malo kajmaka.",
			sum:  "Nanin recept za tijesto za pitu i fil za sirnicu.",
			sent: []string{"notes:note:Nanin recept za pitu"}, filed: true},
		{day: 7, hh: 10, mm: 3, lang: "en", kind: "meeting", title: "Stand-up: login screen done, API keys blocked", tags: "work",
			said: "Stand-up. Lejla finished the login screen. Marko is still blocked on the API keys from the client, I said I'd chase them today. Release candidate on " + time.Now().AddDate(0, 0, -7+8).Format("Monday") + ". Demo for the client the Tuesday after, at eleven.",
			tidy: "Stand-up:\n- Lejla finished the login screen.\n- Marko is still blocked on the API keys from the client; I'll chase them today.\n- Release candidate on " + time.Now().AddDate(0, 0, 1).Format("Monday") + ".\n- Demo for the client the Tuesday after, at 11:00.",
			sum:  "Login screen done; I chase the client's API keys; release candidate next, then a client demo on Tuesday.",
			actions: []store.Action{
				{Type: "task", Title: "Chase the client for the API keys", Due: demoDate(7)},
				{Type: "event", Title: "Client demo", Start: tuesday + "T11:00", End: tuesday + "T12:00"},
			},
			sent: []string{"tasks:task:Chase the client for the API keys", "calendar:event:Client demo"}, filed: true},
		{day: 9, hh: 8, mm: 55, lang: "en", kind: "idea", title: "Garden: raised beds along the fence", tags: "garden, ideas",
			said: "For the garden next spring, raised beds along the fence where the sun is all afternoon. Three of them, maybe two metres each. Tomatoes, peppers, and herbs closest to the kitchen door.",
			tidy: "For the garden next spring: raised beds along the fence, where the sun is all afternoon. Three of them, maybe two metres each: tomatoes, peppers, and herbs closest to the kitchen door.",
			sum:  "Three raised beds along the sunny fence next spring, herbs nearest the kitchen."},
		{day: 11, hh: 17, mm: 20, lang: "en", kind: "todo", title: "Call about the heating before winter", tags: "home",
			said: "Before it gets cold, call the heating guy to service the boiler. Last year he was booked out for three weeks. Also bleed the radiators upstairs.",
			tidy: "Before it gets cold, call the heating technician to service the boiler (last year he was booked out for three weeks). Also bleed the radiators upstairs.",
			actions: []store.Action{
				{Type: "task", Title: "Call the heating technician to service the boiler", Due: demoDate(9)},
				{Type: "task", Title: "Bleed the radiators upstairs"},
			},
			sent: []string{"tasks:task:Call the heating technician to service the boiler"}, filed: true},
		{day: 13, hh: 20, mm: 5, lang: "bs", kind: "journal", title: "Večer sa djecom na Trebeviću", tags: "porodica",
			said:    "Danas smo bili na Trebeviću sa djecom, gore žičarom, pa pješke do bob staze. Mali je pao i oderao koljeno ali nije plakao, ponosan je bio kao da je osvojio medalju. Spustili smo se taman prije mraka.",
			tidy:    "Danas smo bili na Trebeviću sa djecom: gore žičarom, pa pješke do bob staze. Mali je pao i oderao koljeno, ali nije plakao; bio je ponosan kao da je osvojio medalju. Spustili smo se taman prije mraka.",
			sum:     "Popodne sa djecom na Trebeviću i bob stazi.",
			actions: []store.Action{{Type: "journal", Text: "Sa djecom na Trebeviću i bob stazi; mali je pao, ali je bio ponosan kao da je osvojio medalju."}},
			sent:    []string{"notes:daily:Today's note"}, filed: true},
		{day: 16, hh: 12, mm: 40, lang: "en", kind: "note", title: "Book ideas for the reading group", tags: "books",
			said: "Books for the reading group, so I don't forget what people suggested. The Death of Vivek Oji, Lejla. Tomorrow and Tomorrow and Tomorrow, Ana. And Mirza wants something by Ivo Andrić, probably The Bridge on the Drina again.",
			tidy: "Books for the reading group, as people suggested:\n- The Death of Vivek Oji (Lejla)\n- Tomorrow, and Tomorrow, and Tomorrow (Ana)\n- Something by Ivo Andrić, probably The Bridge on the Drina again (Mirza)"},
		{day: 20, hh: 7, mm: 58, lang: "en", kind: "reminder", title: "Mum's birthday present", tags: "family",
			said:    "Mum's birthday is in two weeks. She mentioned the blue scarf at the market, the wool one. Get it before the stall closes for winter.",
			tidy:    "Mum's birthday is in two weeks. She mentioned the blue wool scarf at the market; get it before the stall closes for winter.",
			actions: []store.Action{{Type: "task", Title: "Buy the blue wool scarf for Mum's birthday", Due: demoDate(10)}},
			sent:    []string{"tasks:task:Buy the blue wool scarf for Mum's birthday"}, filed: true},
		{day: 24, hh: 18, mm: 2, lang: "en", kind: "journal", title: "Fuel and the long way home", tags: "car",
			said:    "Filled up the car, sixty-five marks. Took the long way home over Kobilja Glava because of the traffic, actually nice in the evening light.",
			tidy:    "Filled up the car: 65 marks. Took the long way home over Kobilja Glava because of the traffic; it was actually nice in the evening light.",
			actions: []store.Action{{Type: "expense", Title: "Fuel", Amount: 65, Currency: "BAM", Date: demoDate(24)}},
			sent:    []string{"money:expense:Fuel · 65.00 BAM"}, filed: true},
	}
}

// fillers are short memos spread over the months before.
var demoFillers = []struct{ kind, title, text, tags string }{
	{"todo", "Return the library books", "Return the library books before Saturday, two of them are already late.", ""},
	{"idea", "Name for the photo album", "For the summer album: Sea, salt and too much ice cream.", "ideas"},
	{"note", "Wi-Fi password for the cottage", "The cottage Wi-Fi is the one with the long name, the password is on the fridge, not the router.", "home"},
	{"journal", "First snow on Bjelašnica", "First snow on Bjelašnica today, you can see it from the office window.", ""},
	{"todo", "Pay the electricity bill", "Electricity bill came, pay it this week, the app keeps logging me out.", "bills"},
	{"meeting", "Call with the accountant", "Accountant says send the invoices for the quarter by the tenth, and the receipts from the trip separately.", "work"},
	{"shopping", "Hardware store", "Hardware store: light bulbs for the hallway, the warm ones, a new shower head, and screws for the shelf.", "home"},
	{"note", "Running route", "Good running route: along the river from Skenderija to Ilidža, eleven kilometres, flat.", "running"},
	{"reminder", "Passport photos", "Get passport photos for the little one, the passport runs out in spring.", "family"},
	{"idea", "Weekend away in Mostar", "Weekend away in Mostar in the spring, before it gets hot. Stay in the old town.", "travel"},
	{"journal", "Concert at the National Theatre", "Concert at the National Theatre was beautiful, the second half especially. Should go more often.", ""},
	{"todo", "Back up the old laptop", "Back up the old laptop before giving it to my sister, the photos folder especially.", ""},
	{"note", "Gift ideas for Ana", "Gift ideas for Ana: the ceramic mug from the fair, a good notebook, or tickets to the jazz festival.", "gifts"},
	{"meeting", "Neighbours about the parking", "Talked to the neighbours about the parking, we agreed on two spots each, the third one for visitors.", "home"},
	{"journal", "Rainy Sunday", "Rainy Sunday, stayed in, made soup and finally finished the book.", ""},
	{"todo", "Cancel the gym membership", "Cancel the gym membership, I haven't been since summer. They need it in writing.", "bills"},
	{"idea", "Bookshelf in the hallway", "A narrow bookshelf in the hallway would fit, thirty centimetres deep, floor to ceiling.", "home, ideas"},
	{"shopping", "Pharmacy", "Pharmacy: vitamin D, plasters for the kids, and the nasal spray.", ""},
	{"reminder", "Book the table for Saturday", "Book the table at Dveri for Saturday, they fill up on Fridays.", ""},
	{"note", "Bus times to the airport", "The airport bus leaves every hour from the Holiday Inn stop, at ten past.", "travel"},
	{"journal", "Coffee with Lejla", "Coffee with Lejla at Ministry of Ćejf, she's moving to Vienna in March.", ""},
	{"todo", "Fix the kitchen tap", "The kitchen tap drips again, buy the washer or call someone.", "home"},
	{"idea", "Learn to make burek", "Learn to make burek properly this winter, ask the neighbour, she offered.", "recipes, ideas"},
	{"meeting", "Kindergarten plans", "Kindergarten: the winter show is on the eighteenth, bring something white to wear.", "school"},
}

// seedDemo fills the owner's account with three months of voice memos —
// lists, meetings, ideas, journal bits in English and Bosnian — written out,
// tidied, with what was sent where (DEMO_DATA, for `make run`, the CI smoke
// test and the screenshots). Dates are relative to today.
func (s *Server) seedDemo(ctx context.Context, uid int64) error {
	// with AUTH_MODE=none this runs inside the first request; finish even
	// when that client goes away
	ctx = context.WithoutCancel(ctx)
	if ms, _, _ := s.store.ListMemos(ctx, uid, store.MemoFilter{Limit: 1}); len(ms) > 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(42))
	all := demoMemos()
	for i, f := range demoFillers {
		day := 27 + i*3 + rng.Intn(3)
		all = append(all, demoMemo{day: day, hh: 7 + rng.Intn(14), mm: rng.Intn(60), lang: "en", kind: f.kind, title: f.title, said: f.text, tidy: f.text, tags: f.tags, filed: rng.Intn(5) > 0})
	}
	for i, d := range all {
		at := time.Now().AddDate(0, 0, -d.day)
		at = time.Date(at.Year(), at.Month(), at.Day(), d.hh, d.mm, rng.Intn(60), 0, time.Local)
		if at.After(time.Now()) {
			at = time.Now().Add(-time.Duration(10+i) * time.Minute)
		}
		words := len(strings.Fields(d.said))
		dur := int64(float64(words)/2.4*1000) + int64(rng.Intn(2500)) + 1200
		m := &store.Memo{Title: d.title, RecordedAt: at.UTC().Format(time.RFC3339Nano), DurationMS: dur, Language: d.lang, Status: store.StatusDone,
			Transcript: d.said, Segments: demoSegments(d.said, dur), Engine: "whisper " + speech.DefaultModel, Summary: d.sum, Tidy: d.tidy,
			Kind: d.kind, Tags: d.tags, Starred: d.star, Filed: d.filed, Peaks: demoPeaks(rng, d.said), Actions: d.actions}
		if d.tidy != "" || d.sum != "" {
			m.AIStatus, m.AIProvider = "done", "pocket"
		}
		sent := []*store.Sent{}
		for _, x := range d.sent {
			p := strings.SplitN(x, ":", 3)
			sent = append(sent, &store.Sent{App: p[0], Kind: p[1], Title: p[2], Ref: fmt.Sprint(100 + rng.Intn(800)), CreatedAt: at.Add(3 * time.Minute).UTC().Format(time.RFC3339Nano)})
		}
		for j := range m.Actions {
			for _, x := range sent {
				if strings.HasPrefix(x.Title, firstNonEmpty(m.Actions[j].Title, "Today's note")) && demoKind(m.Actions[j].Type) == x.Kind {
					m.Actions[j].SentTo, m.Actions[j].SentRef = x.App, x.Ref
				}
			}
		}
		if i < len(all)-len(demoFillers) {
			if name, size, err := s.demoAudio(uid, m.Peaks, dur); err == nil {
				m.Audio, m.AudioType, m.AudioBytes = name, "audio/wav", size
			}
		}
		if err := s.store.CreateMemo(ctx, uid, m); err != nil {
			return err
		}
		for _, x := range sent {
			x.MemoID = m.ID
			if err := s.store.AddSent(ctx, uid, x); err != nil {
				return err
			}
		}
	}
	if err := s.seedDemoMeeting(ctx, uid, rng); err != nil {
		return err
	}
	n, err := s.seedDemoDictations(ctx, uid, rng)
	if err != nil {
		return err
	}
	s.log.Info("demo data loaded", "memos", len(all), "dictations", n)
	return nil
}

// demoDictated is what the demo person dictated on the Mac with Pocket
// Launcher, and into which app.
var demoDictated = []struct{ app, appID, text string }{
	{"Pocket Projects", "org.bjelke.pocket.projects", "Move the onboarding redesign to next sprint and ask Lejla for the final copy before Thursday."},
	{"Slack", "com.tinyspeck.slackmacgap", "Sounds good, I'll review the pull request after lunch and leave comments on the caching part."},
	{"Mail", "com.apple.mail", "Hi Daniel, thanks for the quick turnaround. The invoice looks right; I'll pay it by the end of the week."},
	{"Messages", "com.apple.MobileSMS", "Running ten minutes late, order me the ćevapi please."},
	{"Notes", "com.apple.Notes", "Ideas for the garden: raised bed by the fence, tomatoes and basil, a rain barrel under the gutter."},
	{"Pocket Projects", "org.bjelke.pocket.projects", "Bug: the export button does nothing on Safari when the list is filtered by tag."},
	{"Slack", "com.tinyspeck.slackmacgap", "Can we move the standup to ten tomorrow? I have the dentist at nine."},
	{"Cursor", "com.todesktop.230313mzl4w4u92", "Write a test that posts the same dictation twice and checks the second answer is the stored one."},
	{"Mail", "com.apple.mail", "Dear Ms. Hodžić, I would be glad to join the panel on the twelfth. Please send the agenda when it is ready."},
	{"Messages", "com.apple.MobileSMS", "Happy birthday! Dinner on Saturday is on me."},
	{"Cursor", "com.todesktop.230313mzl4w4u92", "Refactor the settings page so each section loads on its own and a failing one does not blank the rest."},
	{"Pocket Projects", "org.bjelke.pocket.projects", "Add a checklist to the release task: changelog, tag, deploy, and a note in the team channel."},
}

func (s *Server) seedDemoDictations(ctx context.Context, uid int64, rng *rand.Rand) (int, error) {
	n := 0
	for day := 34; day >= 0; day-- {
		if day == 6 || day == 13 || day == 20 {
			continue // a few days off, so the streak means something
		}
		for k := 2 + rng.Intn(9); k > 0; k-- {
			x := demoDictated[rng.Intn(len(demoDictated))]
			at := time.Now().AddDate(0, 0, -day)
			at = time.Date(at.Year(), at.Month(), at.Day(), 8+rng.Intn(11), rng.Intn(60), rng.Intn(60), 0, time.Local)
			if at.After(time.Now()) {
				at = time.Now().Add(-time.Duration(3+n%50) * time.Minute)
			}
			words := len(strings.Fields(x.text))
			d := &store.Dictation{Ref: fmt.Sprintf("demo-%d", n), Text: x.text, App: x.app, AppID: x.appID, DurationMS: int64(float64(words)/2.5*1000) + int64(rng.Intn(1500)),
				Engine: "parakeet-v3", Language: "en", Device: "Maya’s MacBook Pro", DictatedAt: store.DictationTime(at)}
			if rng.Intn(3) == 0 {
				d.Polished, d.Raw = true, "um so "+strings.ToLower(strings.TrimSuffix(x.text, "."))
			}
			if _, err := s.store.CreateDictation(ctx, uid, d); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

func demoKind(actionType string) string {
	switch actionType {
	case "journal":
		return "daily"
	}
	return actionType
}

// demoSegments splits the words into sentences timed along the recording.
func demoSegments(text string, dur int64) []store.Segment {
	var parts []string
	cur := ""
	for _, w := range strings.Fields(text) {
		cur = strings.TrimSpace(cur + " " + w)
		if strings.HasSuffix(w, ".") || strings.HasSuffix(w, "?") || strings.HasSuffix(w, "!") || len(cur) > 140 {
			parts = append(parts, cur)
			cur = ""
		}
	}
	if cur != "" {
		parts = append(parts, cur)
	}
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	out := []store.Segment{}
	var at int64 = 400
	for _, p := range parts {
		d := int64(float64(dur-800) * float64(len(p)) / float64(max(total, 1)))
		out = append(out, store.Segment{Start: at, End: at + d, Text: p})
		at += d
	}
	return out
}

// demoPeaks is a speech-like loudness line: syllables, words, pauses at full stops.
func demoPeaks(rng *rand.Rand, text string) []int {
	n := 120
	vals := make([]float64, n)
	sentences := strings.Count(text, ".") + 1
	for i := range vals {
		phase := float64(i) / float64(n) * float64(sentences) * math.Pi
		env := 0.35 + 0.65*math.Abs(math.Sin(phase))
		v := env * (0.4 + 0.6*rng.Float64())
		if rng.Intn(11) == 0 {
			v *= 0.2
		}
		vals[i] = v * v
	}
	return speech.ScalePeaks(vals)
}

// demoAudio writes a soft hum that follows the waveform, so the player plays
// something in the demo (no real voices are shipped).
func (s *Server) demoAudio(uid int64, peaks []int, durMS int64) (string, int64, error) {
	if err := os.MkdirAll(s.audioDir(uid), 0o755); err != nil {
		return "", 0, err
	}
	const rate = 8000
	frames := int(durMS * rate / 1000)
	name := randName(".wav")
	f, err := os.Create(s.audioPath(uid, name))
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	data := uint32(frames * 2)
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], 36+data)
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1)
	binary.LittleEndian.PutUint16(hdr[22:], 1)
	binary.LittleEndian.PutUint32(hdr[24:], rate)
	binary.LittleEndian.PutUint32(hdr[28:], rate*2)
	binary.LittleEndian.PutUint16(hdr[32:], 2)
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], data)
	if _, err := f.Write(hdr); err != nil {
		return "", 0, err
	}
	buf := make([]byte, frames*2)
	for i := 0; i < frames; i++ {
		p := float64(peaks[min(len(peaks)-1, i*len(peaks)/max(frames, 1))]) / 100
		t := float64(i) / rate
		v := p * 0.18 * (math.Sin(2*math.Pi*180*t) + 0.5*math.Sin(2*math.Pi*270*t)) * (0.6 + 0.4*math.Sin(2*math.Pi*4*t))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(v*32767)))
	}
	if _, err := f.Write(buf); err != nil {
		return "", 0, err
	}
	return name, int64(44 + len(buf)), nil
}
