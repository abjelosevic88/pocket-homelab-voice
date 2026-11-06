package server

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-voice/internal/speech"
	"github.com/abjelosevic88/pocket-homelab-voice/internal/store"
)

// demoTurns is a team stand-up recorded with Pocket Launcher: who spoke
// ("You", or the call's voices S1…S6) and what they said. Updates are long,
// questions short, as in a real call.
var demoTurns = []struct{ who, text string }{
	{"S1", "Morning everyone. Daniel, your sound is a bit low."},
	{"S2", "Better now? I moved closer to the mic."},
	{"S1", "Much better. Let's go round, launch first. Selma, you start?"},
	{"S4", "Sure. The website copy is done and the last round of images is ready. I'll upload the hero image and the three feature pictures today, before five. The pricing page still has the old screenshots, so I'll redo those tomorrow morning."},
	{"S1", "Great. And the translations?"},
	{"S4", "German is back from the translator, Bosnian I'm checking myself tonight."},
	{"You", "Thanks Selma. From my side, the sign-up flow is finished and merged. I fixed the bug where the confirmation mail went out twice, and I added the welcome screen we talked about last week. Today I'm on the export: people asked for it twice in the beta, so I want it in before the launch."},
	{"S3", "Can the export do CSV as well as PDF?"},
	{"You", "CSV first, PDF next week. I'll send you a test file this afternoon."},
	{"S3", "Perfect, I need it for the accountant's import."},
	{"S2", "Then me. I can put the site live on Tuesday morning. The servers are ready, the backups run every night, and the status page is up. The payment page still needs one more test with a real card."},
	{"You", "Let's not go live without that test. Can you run it on Monday?"},
	{"S2", "Monday afternoon, and I'll post the result in the channel."},
	{"S1", "Good. Mirza, the newsletter?"},
	{"S3", "We have twelve hundred people on the list now, up from nine hundred last month. Most came from the blog post about the beta. I'll write the launch mail by Friday and share the draft with Lejla."},
	{"S1", "Then the mail goes out Tuesday at ten, an hour after the site is up."},
	{"S5", "On support: we had thirty-two tickets this week, most about logging in on the old app. I wrote a help page for it and the number dropped by half since Wednesday. Two people asked about family accounts, I told them it's coming in spring."},
	{"S1", "Is spring still right for family accounts?"},
	{"You", "Spring is fine, but not before the export and the new settings."},
	{"S5", "Then I'll keep saying spring."},
	{"S6", "Small one from me: the app store pictures need the new logo. I can do them on Thursday if someone sends me the final files."},
	{"S4", "I'll send them right after this call."},
	{"S1", "Next, the budget. We spent about four hundred euros on ads in October and got sixty sign-ups, so roughly seven euros each. Two channels worked, the third brought nothing."},
	{"S3", "Let's stop the third one and keep the money on the other two."},
	{"S2", "Agreed. Same four hundred for November?"},
	{"You", "Yes: we stop the third channel and keep four hundred euros for November."},
	{"S5", "One more thing: I'm away the week after next, Monday to Wednesday. Tarik's inbox goes to Selma then."},
	{"S4", "Fine with me."},
	{"S1", "Thanks Tarik. Anything else? Then that's it. Thanks everyone, see you tomorrow."},
	{"You", "Thanks all."},
}

const demoMeetingTidy = `## Launch: website and translations

Lejla: Asked Daniel to fix his low sound, then went round the team, launch first.
Selma: The website copy and the last images are done; the hero image and three feature pictures go up today, before five. The pricing page screenshots are redone tomorrow morning. German is back from the translator; Selma checks Bosnian tonight.

## Sign-up and export

You: The sign-up flow is finished and merged; the double confirmation mail is fixed and the welcome screen is in. Today: the export, asked for twice in the beta, before the launch.
Mirza: Asked for CSV as well as PDF, for the accountant's import.
You: CSV first, PDF next week; a test file this afternoon.

## Going live

Daniel: The site can go live on Tuesday morning: servers, nightly backups and the status page are ready. The payment page needs one more test with a real card.
You: Not live without that test.
Daniel: Runs it Monday afternoon and posts the result in the channel.

## Newsletter

Mirza: 1,200 people on the list, up from 900, mostly from the beta blog post. The launch mail draft goes to Lejla by Friday.
Lejla: The mail goes out on Tuesday at 10, an hour after the site.

## Support and app store

Tarik: 32 tickets this week, mostly logging in on the old app; a help page halved them since Wednesday. Two asked for family accounts: "coming in spring".
You: Spring holds, after the export and the new settings.
Speaker 6: The app store pictures need the new logo; done on Thursday with the final files.
Selma: Sends the files right after the call.

## Budget

Lejla: About €400 on ads in October brought 60 sign-ups, roughly €7 each; two channels worked, the third brought nothing.
Mirza: Stop the third and keep the money on the other two.
You: The third channel stops; €400 for November.

## Wrap-up

Tarik: Away the week after next, Monday to Wednesday; his inbox goes to Selma.`

const demoMeetingSummary = `The launch is set for Tuesday: Daniel puts the site live in the morning once he has tested the payment page with a real card on Monday, and Mirza's launch mail follows at 10. The export (CSV first) lands before the launch, support tickets halved after a new help page, and family accounts stay promised for spring. October's €400 of ads brought 60 sign-ups.

Decisions:
- The site goes live on Tuesday morning, only after the payment test
- The launch mail goes out on Tuesday at 10:00
- The export ships as CSV first, PDF next week
- The ad channel that brought nothing is stopped; €400 for November`

// seedDemoMeeting adds a call written out per voice, with the voices named as
// they would be after a few meetings: one named by you, one recognised, one
// asked about and one with a name heard in the conversation.
func (s *Server) seedDemoMeeting(ctx context.Context, uid int64, rng *rand.Rand) error {
	vec := func() []float32 {
		v := make([]float32, 16)
		for i := range v {
			v[i] = float32(rng.NormFloat64())
		}
		return v
	}
	voices := map[string][]float32{"S1": vec(), "S2": vec(), "S3": vec(), "S4": vec(), "S5": vec(), "S6": vec()}
	people := map[string]*store.Person{}
	for _, p := range []struct {
		name, key string
		meetings  int
	}{{"Lejla", "S1", 6}, {"Daniel", "S2", 4}, {"Mirza", "S3", 5}, {"Selma", "S4", 3}, {"Tarik", "S5", 2}, {"Nina", "S6", 1}} {
		person := &store.Person{Name: p.name}
		for i := 0; i < p.meetings; i++ {
			person.Voices = append(person.Voices, store.Voice{Emb: voices[p.key], MS: int64(40000 + rng.Intn(80000)), MemoID: int64(-1 - i), At: time.Now().AddDate(0, 0, -7*(i+1)).UTC().Format(time.RFC3339Nano)})
		}
		if err := s.store.CreatePerson(ctx, uid, person); err != nil {
			return err
		}
		people[p.key] = person
	}

	// the turns, timed along the call: a stand-up of about twenty minutes
	var segs []store.Segment
	var said []string
	var at int64 = 1200
	for _, t := range demoTurns {
		for _, part := range demoSegments(t.text, int64(float64(len(strings.Fields(t.text)))/2.6*1000*5)+2500) {
			segs = append(segs, store.Segment{Start: at + part.Start, End: at + part.End, Text: part.Text, Speaker: t.who})
		}
		at = segs[len(segs)-1].End + int64(1500+rng.Intn(6000))
		said = append(said, t.text)
	}
	dur := at + 1500
	ms := map[string]int64{}
	sample := map[string][2]int64{}
	for _, sg := range segs {
		ms[sg.Speaker] += sg.End - sg.Start
		if sg.End-sg.Start > sample[sg.Speaker][1]-sample[sg.Speaker][0] {
			sample[sg.Speaker] = [2]int64{sg.Start, sg.End}
		}
	}
	m := &store.Memo{Title: "Team stand-up: launch week", Language: "en", Status: store.StatusDone, Meeting: true, Kind: "meeting", KindSet: true,
		Tags: "work, launch", Engine: "whisper " + speech.DefaultModel, AIStatus: "done", AIProvider: "pocket",
		DurationMS: dur, Segments: segs, Tidy: demoMeetingTidy, Summary: demoMeetingSummary, SpeakerVoices: voices, SpeakersStatus: "done",
		Speakers: []store.Speaker{
			{Key: "You", Name: "You", State: store.SpeakerYou, MS: ms["You"]},
			{Key: "S1", Name: "Lejla", PersonID: people["S1"].ID, State: store.SpeakerConfirmed, MS: ms["S1"], Sample: sample["S1"]},
			{Key: "S2", Name: "Daniel", PersonID: people["S2"].ID, State: store.SpeakerAuto, Score: 0.86, MS: ms["S2"], Sample: sample["S2"]},
			{Key: "S3", Name: "Mirza", PersonID: people["S3"].ID, State: store.SpeakerConfirmed, MS: ms["S3"], Sample: sample["S3"]},
			{Key: "S4", Name: "Selma", PersonID: people["S4"].ID, State: store.SpeakerConfirmed, MS: ms["S4"], Sample: sample["S4"]},
			{Key: "S5", Name: "Tarik", PersonID: people["S5"].ID, State: store.SpeakerAuto, Score: 0.81, MS: ms["S5"], Sample: sample["S5"]},
			{Key: "S6", State: store.SpeakerSuggested, Suggest: "Nina", SuggestID: people["S6"].ID, Score: 0.63, MS: ms["S6"], Sample: sample["S6"],
				Hint: "Nina", HintWhy: "Selma answers this voice with \"I'll send them right after this call\" when it asks for the logo files, and Lejla thanked Nina for the same files last week."},
		},
		Actions: []store.Action{
			{Type: "task", Title: "Send Mirza a CSV test file of the export", Due: demoDate(1)},
			{Type: "task", Title: "Check that Daniel tested the payment page", Due: demoNext(time.Monday), Notes: "With a real card, before the site goes live"},
			{Type: "event", Title: "Website launch", Start: demoNext(time.Tuesday) + "T09:00"},
		}}
	at0 := time.Now().AddDate(0, 0, -1)
	m.RecordedAt = time.Date(at0.Year(), at0.Month(), at0.Day(), 9, 30, 0, 0, time.Local).UTC().Format(time.RFC3339Nano)
	m.Peaks = demoPeaks(rng, strings.Join(said, " "))
	m.Transcript = conversation(segs, func(k string) string { return speakerLabel(m, k) })
	if name, size, err := s.demoAudio(uid, m.Peaks, dur); err == nil {
		m.Audio, m.AudioType, m.AudioBytes = name, "audio/wav", size
	}
	if err := s.store.CreateMemo(ctx, uid, m); err != nil {
		return fmt.Errorf("demo meeting: %w", err)
	}
	return nil
}
