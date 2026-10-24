package campaigns

import (
	"strings"
	"testing"
	"time"
)

func TestWindowOpen(t *testing.T) {
	w, err := NewWindow("Asia/Kolkata", "09:00", "17:00", []int{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatal(err)
	}
	ist := time.FixedZone("IST", 5*3600+1800)
	cases := []struct {
		at   time.Time
		open bool
	}{
		{time.Date(2026, 10, 5, 9, 0, 0, 0, ist), true},      // Monday 09:00
		{time.Date(2026, 10, 5, 16, 59, 0, 0, ist), true},    // Monday 16:59
		{time.Date(2026, 10, 5, 17, 0, 0, 0, ist), false},    // end is exclusive
		{time.Date(2026, 10, 5, 8, 59, 0, 0, ist), false},    // before start
		{time.Date(2026, 10, 4, 12, 0, 0, 0, ist), false},    // Sunday
		{time.Date(2026, 10, 5, 4, 0, 0, 0, time.UTC), true}, // 09:30 IST expressed in UTC
	}
	for _, c := range cases {
		if got := w.Open(c.at); got != c.open {
			t.Errorf("Open(%v) = %v, want %v", c.at, got, c.open)
		}
	}
}

func TestNewWindowValidation(t *testing.T) {
	if _, err := NewWindow("Mars/Base", "09:00", "17:00", []int{1}); err == nil {
		t.Error("want timezone error")
	}
	if _, err := NewWindow("UTC", "17:00", "09:00", []int{1}); err == nil {
		t.Error("want end-before-start error")
	}
	if _, err := NewWindow("UTC", "9am", "17:00", []int{1}); err == nil {
		t.Error("want parse error")
	}
}

func TestPerTick(t *testing.T) {
	if got := PerTick(480, 8*time.Hour, time.Minute); got != 1 {
		t.Errorf("480/day over 8h at 1m ticks = %d, want 1", got)
	}
	if got := PerTick(1000, 8*time.Hour, 30*time.Second); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
	if got := PerTick(50, time.Hour, 2*time.Hour); got != 100 {
		t.Errorf("got %d, want 100 (tick longer than window)", got)
	}
	if got := PerTick(0, time.Hour, time.Minute); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestRenderStepThreadsFollowUps(t *testing.T) {
	r := Recipient{FirstName: "Diya", CompanyName: "Saffron Table", City: "Noida"}
	s := Sender{FromName: "Sam", SenderCompany: "Acme", PhysicalAddress: "1 Road"}
	first := RenderStep(Step{SubjectTemplate: "Idea for {{company_name}}", BodyTemplate: "Hi {{first_name}}"}, "", r, s, "https://u.test/1")
	if first.Subject != "Idea for Saffron Table" {
		t.Fatalf("subject %q", first.Subject)
	}
	follow := RenderStep(Step{BodyTemplate: "Bumping this, {{first_name|there}}"}, first.Subject, r, s, "https://u.test/1")
	if follow.Subject != "Re: Idea for Saffron Table" {
		t.Fatalf("follow-up subject %q", follow.Subject)
	}
	again := RenderStep(Step{BodyTemplate: "x"}, follow.Subject, r, s, "u")
	if again.Subject != "Re: Idea for Saffron Table" {
		t.Fatalf("subject should not stack Re: — got %q", again.Subject)
	}
	for _, body := range []string{first.Text, first.HTML} {
		if !strings.Contains(body, "https://u.test/1") || !strings.Contains(body, "1 Road") {
			t.Fatalf("footer missing from %q", body)
		}
	}
}

func TestExceedsBounceLimit(t *testing.T) {
	if exceedsBounceLimit(10, 5, 0.05) {
		t.Error("tiny sample should not trip the limit")
	}
	if !exceedsBounceLimit(100, 6, 0.05) {
		t.Error("6% > 5% should trip")
	}
	if exceedsBounceLimit(100, 5, 0.05) {
		t.Error("exactly at the limit should not trip")
	}
}
