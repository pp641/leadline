package inbox

import (
	"context"
	"strings"
	"testing"

	"leadline/internal/integrations"
	"leadline/internal/replies"
)

const reply = "From: Diya Mehta <Diya@Saffron.example>\r\n" +
	"To: hello@sender.test\r\n" +
	"Subject: Re: Idea for Saffron Table\r\n" +
	"Message-ID: <abc123@mail.saffron.example>\r\n" +
	"In-Reply-To: <orig-1@sender.test>\r\n" +
	"References: <orig-0@sender.test> <orig-1@sender.test>\r\n" +
	"Content-Type: multipart/alternative; boundary=\"b1\"\r\n\r\n" +
	"--b1\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" +
	"Sounds great, can you send me pricing? We open a second branch soon=2E\r\n\r\n" +
	"On Mon, 5 Oct 2026 at 10:00, Sam <hello@sender.test> wrote:\r\n> Hi Diya,\r\n> quoted text\r\n" +
	"--b1\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Sounds great</p>\r\n--b1--\r\n"

func TestParseReply(t *testing.T) {
	p, err := Parse([]byte(reply))
	if err != nil {
		t.Fatal(err)
	}
	if p.MessageID != "abc123@mail.saffron.example" || p.InReplyTo != "orig-1@sender.test" || len(p.References) != 2 {
		t.Fatalf("ids: %+v", p)
	}
	if p.FromEmail != "diya@saffron.example" || p.FromName != "Diya Mehta" {
		t.Fatalf("from: %q %q", p.FromEmail, p.FromName)
	}
	if strings.Contains(p.Text, "quoted text") || !strings.Contains(p.Text, "send me pricing? We open a second branch soon.") {
		t.Fatalf("text: %q", p.Text)
	}
	if p.AutoReply || p.Bounce {
		t.Fatal("misdetected as auto-reply/bounce")
	}
}

func TestParseAutoReplyAndBounce(t *testing.T) {
	ooo := "From: a@b.example\r\nSubject: Automatic reply: Idea\r\nAuto-Submitted: auto-replied\r\nMessage-ID: <x@b>\r\n\r\nI am away until Monday.\r\n"
	p, _ := Parse([]byte(ooo))
	if !p.AutoReply {
		t.Fatal("auto-reply not detected")
	}
	dsn := "From: Mail Delivery Subsystem <MAILER-DAEMON@mx.example>\r\nSubject: Delivery Status Notification (Failure)\r\n" +
		"Message-ID: <dsn1@mx>\r\nContent-Type: multipart/report; report-type=delivery-status; boundary=\"z\"\r\n\r\n" +
		"--z\r\nContent-Type: text/plain\r\n\r\nAddress not found.\r\n--z\r\nContent-Type: message/delivery-status\r\n\r\n" +
		"Final-Recipient: rfc822; jo@nope.example\r\nStatus: 5.1.1\r\n--z\r\nContent-Type: text/rfc822-headers\r\n\r\n" +
		"Message-ID: <orig-9@sender.test>\r\n--z--\r\n"
	p, _ = Parse([]byte(dsn))
	if !p.Bounce || !p.BounceHard || len(p.EmbeddedIDs) == 0 || p.EmbeddedIDs[len(p.EmbeddedIDs)-1] != "orig-9@sender.test" {
		t.Fatalf("bounce: %+v", p)
	}
	soft := strings.Replace(dsn, "Status: 5.1.1", "Status: 4.2.2", 1)
	if p, _ = Parse([]byte(soft)); p.BounceHard {
		t.Fatal("4.x.x should be a soft bounce")
	}
}

func TestRulesClassifier(t *testing.T) {
	cases := map[string]string{
		"Please remove me from your list":          "unsubscribe",
		"STOP emailing me":                         "unsubscribe",
		"I'm out of the office until the 12th":     "out_of_office",
		"Not interested, thanks":                   "not_interested",
		"Maybe next quarter":                       "not_now",
		"Sure, let's talk on Tuesday":              "interested",
		"What does it cost?":                       "question",
		"Ok":                                       "unknown",
		"He left the company, reach out to Priya.": "wrong_person",
	}
	for body, want := range cases {
		got, _ := Rules{}.Classify(context.Background(), replies.InboundEmail{TextBody: body})
		if got.Label != want {
			t.Errorf("%q: got %s want %s", body, got.Label, want)
		}
	}
}

type badLLM struct{}

func (badLLM) Generate(context.Context, integrations.LLMRequest) (integrations.LLMResponse, error) {
	return integrations.LLMResponse{Content: `{"classification":"positive","confidence":0.9,"summary":"x"}`}, nil
}

func TestOptOutsNeverDependOnAI(t *testing.T) {
	// Even if the model says "positive", an explicit opt-out wins.
	c, by := Classify(context.Background(), badLLM{}, replies.InboundEmail{TextBody: "unsubscribe me please"})
	if c.Label != "unsubscribe" || by != "rules" {
		t.Fatalf("got %s by %s", c.Label, by)
	}
	c, by = Classify(context.Background(), badLLM{}, replies.InboundEmail{TextBody: "Let's chat next week"})
	if c.Label != "positive" || by != "ai" {
		t.Fatalf("got %s by %s", c.Label, by)
	}
}
