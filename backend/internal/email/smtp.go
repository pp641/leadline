package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"time"

	"leadline/pkg/safehttp"
)

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	TLS      string // "none" | "starttls" | "tls"
	// Guarded refuses connections to non-public addresses; set for
	// user-configured hosts (per-mailbox SMTP) to prevent SSRF.
	Guarded bool
}

// SMTP sends through any SMTP relay (Mailpit locally; SES/SendGrid/Mailgun/
// Postmark all expose SMTP too). One connection per message keeps it simple
// and robust; pooling can be added behind the same interface.
type SMTP struct{ cfg SMTPConfig }

func NewSMTP(cfg SMTPConfig) *SMTP { return &SMTP{cfg: cfg} }

func (s *SMTP) Name() string { return "smtp" }

func (s *SMTP) Send(ctx context.Context, msg EmailMessage) (SendResult, error) {
	raw, err := BuildMIME(msg, time.Now())
	if err != nil {
		return SendResult{}, &SendError{Permanent: true, Err: err}
	}
	if err := s.send(ctx, msg.FromEmail, msg.ToEmail, raw); err != nil {
		return SendResult{}, classify(err)
	}
	return SendResult{Provider: s.Name(), ProviderMessageID: msg.MessageID}, nil
}

// connect dials, upgrades to TLS and authenticates.
func (s *SMTP) connect(ctx context.Context) (*smtp.Client, error) {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	if s.cfg.Guarded {
		dialer = safehttp.Dialer()
	}
	var conn net.Conn
	var err error
	if s.cfg.TLS == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(60 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if s.cfg.TLS == "starttls" {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			c.Close()
			return nil, err
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			c.Close()
			return nil, err
		}
	}
	return c, nil
}

// Check verifies connectivity and credentials without sending mail.
func (s *SMTP) Check(ctx context.Context) error {
	c, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}

func (s *SMTP) send(ctx context.Context, from, to string, raw []byte) error {
	c, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// classify maps SMTP reply codes: 5xx is permanent (and 550-553 on RCPT
// means the address is bad), 4xx and network errors are transient.
func classify(err error) error {
	var tp *textproto.Error
	if errors.As(err, &tp) {
		code := strconv.Itoa(tp.Code)
		switch tp.Code {
		case 530, 534, 535, 538: // authentication required / failed
			return &SendError{ProviderFault: true, Code: code, Err: err}
		}
		if tp.Code >= 500 {
			bounce := tp.Code >= 550 && tp.Code <= 553
			return &SendError{Permanent: true, Bounce: bounce, Code: code, Err: err}
		}
		return &SendError{Code: code, Err: err}
	}
	return &SendError{Err: err}
}

// Log is a development provider that only logs metadata (never bodies).
type Log struct{ log *slog.Logger }

func NewLog(l *slog.Logger) *Log { return &Log{log: l} }

func (l *Log) Name() string { return "log" }

func (l *Log) Send(_ context.Context, msg EmailMessage) (SendResult, error) {
	l.log.Info("email (log provider): not delivered", "message_id", msg.MessageID, "subject_len", len(msg.Subject))
	return SendResult{Provider: l.Name(), ProviderMessageID: msg.MessageID}, nil
}

// NewProvider picks the configured provider. Adding SES/SendGrid API
// providers means adding a case here; nothing else changes.
func NewProvider(name string, smtpCfg SMTPConfig, log *slog.Logger) (EmailProvider, error) {
	switch name {
	case "smtp":
		return NewSMTP(smtpCfg), nil
	case "log":
		return NewLog(log), nil
	}
	return nil, fmt.Errorf("unknown EMAIL_PROVIDER %q (supported: smtp, log)", name)
}
