// Command seed loads demo data, or generates a synthetic CSV for import tests.
//
//	seed                          # demo user, organization, mailbox, 200 leads, a draft campaign
//	seed -csv leads.csv -n 10000  # write a synthetic CSV (no database needed)
//
// All generated companies and people are fictitious and use reserved
// *.example domains, so nothing can reach a real inbox.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // campaign timezones must resolve even in minimal images

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"leadline/config"
	"leadline/internal/app"
	"leadline/internal/auth"
	"leadline/internal/campaigns"
	"leadline/internal/contacts"
	"leadline/internal/icp"
	"leadline/internal/leads"
	"leadline/internal/mailboxes"
	"leadline/internal/organization"
	"leadline/pkg/database"
	"leadline/pkg/logger"
)

const (
	demoEmail    = "demo@leadline.test"
	demoPassword = "demo-password-123"
)

var (
	prefixes   = []string{"Spice", "Urban", "Royal", "Green", "Golden", "Blue", "Saffron", "Tandoor", "Masala", "Copper", "Little", "Old", "Café", "Rustic", "Coastal", "Smoky", "Lotus", "Monsoon"}
	suffixes   = []string{"Kitchen", "Bistro", "House", "Table", "Grill", "Dhaba", "Eatery", "Café", "Diner", "Bakery", "Brewhouse", "Kitchen & Bar", "Tiffin Co", "Social"}
	industries = []string{"restaurant", "restaurant", "restaurant", "cafe", "bakery", "catering", "cloud kitchen"}
	cities     = []struct{ city, state string }{{"Delhi", "Delhi"}, {"Noida", "Uttar Pradesh"}, {"Gurgaon", "Haryana"}, {"Ghaziabad", "Uttar Pradesh"}, {"Faridabad", "Haryana"}}
	firstNames = []string{"Aarav", "Vivaan", "Aditya", "Diya", "Ananya", "Ishaan", "Kabir", "Meera", "Riya", "Arjun", "Saanvi", "Rohan", "Priya", "Neha", "Karan", "Tara"}
	lastNames  = []string{"Sharma", "Verma", "Gupta", "Mehta", "Kapoor", "Singh", "Malhotra", "Bansal", "Chopra", "Arora", "Khanna", "Joshi"}
	titles     = []string{"Founder", "Owner", "Co-founder", "General Manager", "Marketing Manager", "Operations Head"}
)

type fakeLead struct {
	company, website, email, first, last, title, industry, city, state, phone string
	employees, founded                                                        int
}

func fake(r *rand.Rand, i int) fakeLead {
	name := fmt.Sprintf("%s %s", prefixes[r.IntN(len(prefixes))], suffixes[r.IntN(len(suffixes))])
	if i >= len(prefixes)*len(suffixes)/2 {
		name = fmt.Sprintf("%s %d", name, i)
	}
	slug := strings.NewReplacer(" ", "", "&", "and", "é", "e").Replace(strings.ToLower(name))
	c := cities[r.IntN(len(cities))]
	f := fakeLead{
		company: name, industry: industries[r.IntN(len(industries))], city: c.city, state: c.state,
		first: firstNames[r.IntN(len(firstNames))], last: lastNames[r.IntN(len(lastNames))],
		title: titles[r.IntN(len(titles))], employees: 5 + r.IntN(80), founded: 2015 + r.IntN(11),
		phone: fmt.Sprintf("+91 9%09d", r.IntN(1_000_000_000)),
	}
	if r.IntN(3) > 0 { // a third have no website — the ICP from the brief
		f.website = "https://www." + slug + ".example"
	}
	if r.IntN(10) > 0 {
		f.email = strings.ToLower(f.first) + "@" + slug + ".example"
	}
	return f
}

func main() {
	csvPath := flag.String("csv", "", "write a synthetic CSV to this path instead of seeding the database")
	n := flag.Int("n", 10000, "rows for -csv")
	flag.Parse()

	if *csvPath != "" {
		if err := writeCSV(*csvPath, *n); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %d rows to %s\n", *n, *csvPath)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := logger.New("warn")
	slog.SetDefault(log)
	ctx := context.Background()
	a, err := app.New(ctx, cfg, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer a.Close()
	if err := seed(ctx, a); err != nil {
		fmt.Fprintln(os.Stderr, "seed failed:", err)
		os.Exit(1)
	}
}

func writeCSV(path string, n int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"company_name", "website", "email", "first_name", "last_name", "job_title", "industry", "city", "state", "country", "phone", "employees", "founded"})
	r := rand.New(rand.NewPCG(42, 7))
	for i := 0; i < n; i++ {
		l := fake(r, i)
		_ = w.Write([]string{l.company, l.website, l.email, l.first, l.last, l.title, l.industry, l.city, l.state, "India", l.phone,
			strconv.Itoa(l.employees), strconv.Itoa(l.founded)})
	}
	w.Flush()
	return w.Error()
}

func seed(ctx context.Context, a *app.App) error {
	var exists bool
	if err := a.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)`, demoEmail).Scan(&exists); err != nil {
		return err
	}
	if exists {
		fmt.Println("demo data already present; nothing to do")
		return nil
	}

	res, err := a.Auth.Register(ctx, auth.RegisterInput{Email: demoEmail, Password: demoPassword, FullName: "Demo User", AcceptTerms: true}, auth.ClientInfo{})
	if err != nil {
		return err
	}
	org, err := a.Orgs.Create(ctx, res.User.ID, organization.CreateInput{
		Name: "Demo Agency", LegalName: "Demo Agency Pvt Ltd", PhysicalAddress: "221 Example Street, Connaught Place, New Delhi 110001, India",
	})
	if err != nil {
		return err
	}
	p := auth.Principal{UserID: res.User.ID, OrgID: org.ID, Role: auth.RoleOwner}

	mb, err := a.Mailboxes.Create(ctx, p, mailboxes.Input{
		Name: "Outreach", FromEmail: "hello@demo-agency.test", FromName: "Demo User", DailyLimit: 200,
	})
	if err != nil {
		return err
	}

	recentYear := time.Now().Year() - 2
	r := rand.New(rand.NewPCG(1, 2))
	var ids []uuid.UUID
	err = database.WithTx(ctx, a.Pool, func(tx pgx.Tx) error {
		for i := 0; i < 200; i++ {
			l := fake(r, i)
			emp, founded := l.employees, l.founded
			in := &leads.Input{CompanyName: l.company, Website: l.website, Industry: l.industry, City: l.city, State: l.state,
				Country: "India", Phone: l.phone, EmployeeCount: &emp, FoundedYear: &founded, Source: "seed"}
			domain, err := leads.Prepare(in)
			if err != nil {
				return err
			}
			id, err := leads.Insert(ctx, tx, org.ID, in, domain, nil)
			if err != nil {
				return err
			}
			ids = append(ids, id)
			if l.email != "" {
				ci := &contacts.Input{FirstName: l.first, LastName: l.last, JobTitle: l.title, Email: l.email, Source: "seed", IsPrimary: true}
				if err := ci.Prepare(); err != nil {
					return err
				}
				cid, err := contacts.Insert(ctx, tx, org.ID, id, ci)
				if err != nil {
					return err
				}
				// Seeded addresses are fictitious; mark them verified so the demo flows.
				if _, err := tx.Exec(ctx, `UPDATE lead_contacts SET email_status='valid', email_verified_at=now(),
					verification_details='{"provider":"seed","reason":"demo data"}' WHERE id=$1`, cid); err != nil {
					return err
				}
			}
			if err := leads.AddTags(ctx, tx, org.ID, []uuid.UUID{id}, []string{"delhi-ncr"}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := a.Scoring.ScoreLeads(ctx, org.ID, ids); err != nil {
		return err
	}

	if _, err := a.ICPs.Save(ctx, p, nil, icp.Input{Name: "New Delhi NCR restaurants", IsDefault: true, Criteria: icp.Criteria{
		Industries: []string{"restaurant", "cafe", "bakery"}, Cities: []string{"Delhi", "Noida", "Gurgaon", "Ghaziabad", "Faridabad"},
		FoundedAfterYear: &recentYear, Website: "missing_or_poor",
	}}); err != nil {
		return err
	}

	limit := 40
	c, err := a.Campaigns.Create(ctx, p, campaigns.Input{
		Name: "Delhi NCR restaurants — website refresh", MailboxID: &mb.ID, DailyLimit: &limit,
		Timezone: "Asia/Kolkata", SendWindowStart: "00:00", SendWindowEnd: "23:59", SendDays: []int{1, 2, 3, 4, 5, 6, 7},
	})
	if err != nil {
		return err
	}
	_, err = a.Campaigns.ReplaceSteps(ctx, p, c.ID, []campaigns.StepInput{
		{DelayDays: 0, SubjectTemplate: "Quick idea for {{company_name}}",
			BodyTemplate: "Hi {{first_name|there}},\n\nI came across {{company_name}} in {{city}} and had a quick idea for helping more diners find you online.\n\nWe build fast, mobile-friendly websites for restaurants, with online menus and table bookings. Would a short call next week be useful?\n\nBest,\n{{sender_name}}"},
		{DelayDays: 3, BodyTemplate: "Hi {{first_name|there}},\n\nJust floating this back to the top of your inbox — happy to share a few examples from other {{industry}} owners in {{city}}.\n\n{{sender_name}}"},
		{DelayDays: 4, BodyTemplate: "Hi {{first_name|there}},\n\nI won't keep following up. If a new website for {{company_name}} is ever on the list, just reply and I'll send over ideas.\n\n{{sender_name}}"},
	})
	if err != nil {
		return err
	}
	if _, err := a.Campaigns.AddLeads(ctx, p, c.ID, campaigns.AudienceInput{LeadIDs: ids[:50]}); err != nil {
		return err
	}

	fmt.Printf("seeded demo data\n  login:    %s / %s\n  org:      %s\n  leads:    %d\n  campaign: %s (draft, 50 leads, 3 steps)\n",
		demoEmail, demoPassword, org.Name, len(ids), c.Name)
	return nil
}
