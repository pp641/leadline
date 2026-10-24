package campaigns

import (
	"strings"

	"leadline/internal/email"
)

// Recipient carries the lead/contact data available to templates.
type Recipient struct {
	FirstName, LastName, JobTitle                        string
	CompanyName, Website, Industry, City, State, Country string
	PersonalizedLine                                     string // approved AI opener, if any
}

// Sender carries mailbox and organization identity.
type Sender struct {
	FromName, FromEmail, SenderCompany, PhysicalAddress string
}

func vars(r Recipient, s Sender) map[string]string {
	return map[string]string{
		"first_name": r.FirstName, "last_name": r.LastName,
		"full_name": strings.TrimSpace(r.FirstName + " " + r.LastName), "job_title": r.JobTitle,
		"company_name": r.CompanyName, "website": r.Website, "industry": r.Industry,
		"city": r.City, "state": r.State, "country": r.Country,
		"sender_name": s.FromName, "sender_company": s.SenderCompany,
		"personalized_line": r.PersonalizedLine,
	}
}

type Rendered struct {
	Subject        string   `json:"subject"`
	Text           string   `json:"text"`
	HTML           string   `json:"html"`
	EmptyVariables []string `json:"empty_variables"`
}

// RenderStep renders a sequence step. Follow-ups with an empty subject reply
// in the same thread ("Re: <previous subject>"). The compliance footer
// (sender identity + unsubscribe link) is always appended.
func RenderStep(step Step, prevSubject string, r Recipient, s Sender, unsubscribeURL string) Rendered {
	v := vars(r, s)
	var empty []string
	subject := step.SubjectTemplate
	if strings.TrimSpace(subject) == "" && prevSubject != "" {
		subject = "Re: " + strings.TrimPrefix(prevSubject, "Re: ")
	} else {
		var e []string
		subject, e = email.Render(subject, v)
		empty = append(empty, e...)
	}
	body, e := email.Render(step.BodyTemplate, v)
	empty = append(empty, e...)

	footer := email.Footer{SenderCompany: s.SenderCompany, PhysicalAddress: s.PhysicalAddress, UnsubscribeURL: unsubscribeURL}
	return Rendered{
		Subject:        strings.TrimSpace(subject),
		Text:           strings.TrimRight(body, "\n ") + footer.Text(),
		HTML:           email.WrapHTML(email.TextToHTML(body) + footer.HTML()),
		EmptyVariables: dedupe(empty),
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
