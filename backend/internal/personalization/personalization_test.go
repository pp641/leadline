package personalization

import (
	"strings"
	"testing"
)

func TestCleanLine(t *testing.T) {
	if got := cleanLine("  \"Saw   Saffron Table opened in Noida this year.\"\n"); got != "Saw Saffron Table opened in Noida this year." {
		t.Fatalf("got %q", got)
	}
	if cleanLine(strings.Repeat("x", 300)) != "" {
		t.Fatal("over-long output must be rejected")
	}
}
