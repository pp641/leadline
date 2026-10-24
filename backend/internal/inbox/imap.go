package inbox

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"leadline/pkg/safehttp"
)

type IMAPConfig struct {
	Host, Username, Password string
	Port                     int
	TLS                      bool
}

type fetched struct {
	UID uint32
	Raw []byte
}

type syncState struct {
	UIDValidity uint32
	LastUID     uint32
}

// fetchNew reads messages newer than state from INBOX, read-only (it never
// changes flags or deletes). First sync / UIDVALIDITY change: last 7 days only.
func fetchNew(cfg IMAPConfig, state syncState, max int) ([]fetched, syncState, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := safehttp.Dialer()
	var c *client.Client
	var err error
	if cfg.TLS {
		c, err = client.DialWithDialerTLS(dialer, addr, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		c, err = client.DialWithDialer(dialer, addr)
		if err == nil {
			if ok, _ := c.SupportStartTLS(); ok {
				err = c.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
			}
		}
	}
	if err != nil {
		return nil, state, fmt.Errorf("connect: %w", err)
	}
	c.Timeout = 60 * time.Second
	defer c.Logout() //nolint:errcheck
	if err := c.Login(cfg.Username, cfg.Password); err != nil {
		return nil, state, fmt.Errorf("login: %w", err)
	}
	mbox, err := c.Select("INBOX", true)
	if err != nil {
		return nil, state, fmt.Errorf("select inbox: %w", err)
	}

	crit := imap.NewSearchCriteria()
	fresh := state.UIDValidity != mbox.UidValidity || state.LastUID == 0
	if fresh {
		state = syncState{UIDValidity: mbox.UidValidity}
		crit.Since = time.Now().AddDate(0, 0, -7)
	} else {
		crit.Uid = new(imap.SeqSet)
		crit.Uid.AddRange(state.LastUID+1, 0) // 0 = "*"
	}
	uids, err := c.UidSearch(crit)
	if err != nil {
		return nil, state, fmt.Errorf("search: %w", err)
	}
	var want []uint32
	for _, u := range uids {
		if u > state.LastUID { // "n:*" always matches the last message
			want = append(want, u)
		}
	}
	if len(want) > max {
		want = want[:max] // oldest first; the rest next time
	}
	if len(want) == 0 {
		return nil, state, nil
	}
	set := new(imap.SeqSet)
	set.AddNum(want...)
	section := &imap.BodySectionName{Peek: true}
	msgs := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() { done <- c.UidFetch(set, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, msgs) }()
	var out []fetched
	for m := range msgs {
		body := m.GetBody(section)
		if body == nil {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(body, 10<<20))
		if err != nil {
			continue
		}
		out = append(out, fetched{UID: m.Uid, Raw: raw})
		if m.Uid > state.LastUID {
			state.LastUID = m.Uid
		}
	}
	if err := <-done; err != nil {
		return out, state, fmt.Errorf("fetch: %w", err)
	}
	return out, state, nil
}

// checkIMAP verifies that credentials work (connect, login, select).
func checkIMAP(cfg IMAPConfig) error {
	if cfg.Host == "" {
		return errors.New("IMAP host is not set")
	}
	_, _, err := fetchNew(cfg, syncState{UIDValidity: 1, LastUID: ^uint32(0) - 1}, 0)
	return err
}
