package integrations

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"leadline/internal/email"
)

// SMTPFor turns an email-sending integration into SMTP settings. Every
// supported service exposes an authenticated SMTP relay, so campaigns reuse
// the one SMTP transport (with its bounce/fault classification).
func SMTPFor(kind string, c Credentials) (email.SMTPConfig, error) {
	cfg := email.SMTPConfig{Port: 587, TLS: "starttls", Username: c.Get("username"), Password: c.Get("password"), Guarded: true}
	switch kind {
	case KindSES:
		cfg.Host = "email-smtp." + c.Get("region") + ".amazonaws.com"
	case KindSendGrid:
		cfg.Host, cfg.Username = "smtp.sendgrid.net", "apikey"
	case KindMailgun:
		cfg.Host = "smtp.mailgun.org"
		if c.Get("region") == "eu" {
			cfg.Host = "smtp.eu.mailgun.org"
		}
	case KindPostmark:
		cfg.Host, cfg.Username = "smtp.postmarkapp.com", c.Get("password") // token is both username and password
	case KindMicrosoft365:
		cfg.Host = "smtp.office365.com"
	case KindGmail:
		cfg.Host = "smtp.gmail.com"
	case KindSMTP:
		cfg.Host, cfg.TLS = c.Get("host"), c.Get("tls")
		if cfg.TLS == "" {
			cfg.TLS = "starttls"
		}
		if p, err := strconv.Atoi(c.Get("port")); err == nil && p > 0 && p < 65536 {
			cfg.Port = p
		}
	default:
		return cfg, fmt.Errorf("%s is not an email sending integration", kind)
	}
	if cfg.Host == "" || cfg.Host == "email-smtp..amazonaws.com" {
		return cfg, fmt.Errorf("%s: SMTP host is not configured", kind)
	}
	return cfg, nil
}

// OrgSMTP returns the organization's default sending integration — the most
// recently saved active one with "use for mailboxes without their own SMTP"
// on — or ok=false when there is none.
func (r *Registry) OrgSMTP(ctx context.Context, orgID uuid.UUID) (cfg email.SMTPConfig, kind string, ok bool, err error) {
	kinds, err := r.ConnectedWith(ctx, orgID, CapEmail)
	if err != nil {
		return cfg, "", false, err
	}
	for _, k := range kinds {
		creds, err := r.Credentials(ctx, orgID, k)
		if err != nil {
			continue
		}
		if creds["default_sender"] == "false" {
			continue
		}
		cfg, err = SMTPFor(k, creds)
		if err != nil {
			return cfg, k, false, err
		}
		return cfg, k, true, nil
	}
	return cfg, "", false, nil
}
