package email

import (
	"errors"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestRenderWithFallbacks(t *testing.T) {
	out, empty := Render("Hi {{ first_name | there }}, {{company_name}} in {{city}}", map[string]string{"company_name": "Acme"})
	if out != "Hi there, Acme in " {
		t.Fatalf("got %q", out)
	}
	if len(empty) != 1 || empty[0] != "city" {
		t.Fatalf("empty = %v", empty)
	}
}

func TestValidateTemplate(t *testing.T) {
	if err := ValidateTemplate("Hi {{first_name}} at {{company_name|your team}}"); err != nil {
		t.Fatal(err)
	}
	err := ValidateTemplate("{{password}} {{ secret }}")
	if err == nil || !strings.Contains(err.Error(), "password") || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("want unknown variable error, got %v", err)
	}
}

func TestTextToHTMLEscapes(t *testing.T) {
	got := TextToHTML("Hello <b>you</b>\nsee https://x.example/a?b=1&c=2\n\nBye")
	if strings.Contains(got, "<b>") {
		t.Fatalf("unescaped HTML: %s", got)
	}
	if !strings.Contains(got, `<a href="https://x.example/a?b=1&amp;c=2">`) {
		t.Fatalf("link missing: %s", got)
	}
	if strings.Count(got, "<p>") != 2 {
		t.Fatalf("want 2 paragraphs: %s", got)
	}
}

func TestBuildMIMEPreventsHeaderInjection(t *testing.T) {
	raw, err := BuildMIME(EmailMessage{
		MessageID: "abc@x.test", FromEmail: "a@x.test", FromName: "A", ToEmail: "b@y.test",
		Subject: "Hello\r\nBcc: victim@z.test", TextBody: "t", HTMLBody: "<p>t</p>",
		UnsubscribeURL: "https://app.test/u/tok", InReplyTo: "prev@x.test",
	}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	header, _, _ := strings.Cut(s, "\r\n\r\n")
	if strings.Contains(header, "\r\nBcc:") {
		t.Fatalf("header injection succeeded:\n%s", header)
	}
	for _, want := range []string{
		"Message-ID: <abc@x.test>", "In-Reply-To: <prev@x.test>", "List-Unsubscribe: <https://app.test/u/tok>",
		"List-Unsubscribe-Post: List-Unsubscribe=One-Click", "multipart/alternative",
	} {
		if !strings.Contains(header, want) {
			t.Errorf("missing %q in header:\n%s", want, header)
		}
	}
}

func TestClassifySMTPErrors(t *testing.T) {
	cases := []struct {
		err               error
		permanent, bounce bool
	}{
		{&textproto.Error{Code: 550, Msg: "no such user"}, true, true},
		{&textproto.Error{Code: 554, Msg: "policy"}, true, false},
		{&textproto.Error{Code: 451, Msg: "try later"}, false, false},
		{&textproto.Error{Code: 530, Msg: "Authentication required"}, false, false},
		{errors.New("connection refused"), false, false},
	}
	for _, c := range cases {
		se, ok := AsSendError(classify(c.err))
		if !ok || se.Permanent != c.permanent || se.Bounce != c.bounce || se.ProviderFault != (se.Code == "530") {
			t.Errorf("%v: got %+v", c.err, se)
		}
	}
}

func TestFooter(t *testing.T) {
	f := Footer{SenderCompany: "Acme <Ltd>", PhysicalAddress: "1 Road", UnsubscribeURL: "https://u.test/x"}
	if !strings.Contains(f.Text(), "Acme <Ltd>, 1 Road") || !strings.Contains(f.Text(), "https://u.test/x") {
		t.Fatalf("text footer: %q", f.Text())
	}
	if strings.Contains(f.HTML(), "<Ltd>") {
		t.Fatalf("html footer not escaped: %q", f.HTML())
	}
}
