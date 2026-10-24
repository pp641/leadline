package geo

import (
	"slices"
	"testing"
)

func TestCode(t *testing.T) {
	for in, want := range map[string]string{"India": "IN", "IN": "IN", " india ": "IN", "in": "IN", "USA": "US", "United Kingdom": "GB", "Atlantis": ""} {
		if got := Code(in); got != want {
			t.Errorf("Code(%q) = %q, want %q", in, got, want)
		}
	}
	if !Same("India", "IN") || Same("India", "Nepal") || !Same("Atlantis", "atlantis") {
		t.Error("Same")
	}
	v := Variants([]string{"India"})
	for _, want := range []string{"india", "in", "bharat"} {
		if !slices.Contains(v, want) {
			t.Errorf("Variants(India) = %v, missing %q", v, want)
		}
	}
}
