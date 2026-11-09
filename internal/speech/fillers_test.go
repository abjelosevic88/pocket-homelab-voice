package speech

import "testing"

func TestDropFillers(t *testing.T) {
	cases := map[string]string{
		"Yeah, please do a bit more digging because Heidi needs to know uh more details.": "Yeah, please do a bit more digging because Heidi needs to know more details.",
		"Okay, uh do your checks right now. Is this uh this should be VS production I think": "Okay, do your checks right now. Is this this should be VS production I think",
		"Uh, so we go.":            "So we go.",
		"Fine. Um, I think so.":    "Fine. I think so.",
		"I think, um.":             "I think.",
		"like uh uh uh":            "like",
		"like, uh":                 "like",
		"Uh-huh, mhm, that works.": "Uh-huh, mhm, that works.",
		"Hmm? What":                "What",
		"Ovaj, eee, idemo sutra.":  "Ovaj, idemo sutra.",
		"Uh.":                      "",
		"no fillers here":          "no fillers here",
		"one uh\ntwo um three":     "one\ntwo three",
		"umbrella and uhura":       "umbrella and uhura",
	}
	for in, want := range cases {
		if got := DropFillers(in); got != want {
			t.Errorf("DropFillers(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDropFillersResult(t *testing.T) {
	res := &Result{Text: "Uh, hello um there", Segments: []Segment{{Text: "Uh,"}, {Text: "hello um there"}}}
	DropFillersResult(res)
	if res.Text != "Hello there" || len(res.Segments) != 1 || res.Segments[0].Text != "hello there" {
		t.Fatalf("got %+v", res)
	}
}
