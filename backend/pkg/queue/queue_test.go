package queue

import (
	"errors"
	"testing"
	"time"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	for attempt, base := range map[int]time.Duration{1: 15 * time.Second, 3: 60 * time.Second, 20: time.Hour} {
		for i := 0; i < 50; i++ {
			d := Backoff(attempt)
			if d < time.Duration(float64(base)*0.8) || d > time.Duration(float64(base)*1.2) {
				t.Fatalf("Backoff(%d) = %v, want %v ±20%%", attempt, d, base)
			}
		}
	}
}

func TestPermanent(t *testing.T) {
	err := Permanent(errors.New("bad payload"))
	if !IsPermanent(err) || IsPermanent(errors.New("x")) {
		t.Fatal("IsPermanent wrong")
	}
	if (Job{Attempt: 3, MaxAttempts: 3}).IsFinalAttempt() != true {
		t.Fatal("final attempt")
	}
}
