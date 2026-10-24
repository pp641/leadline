package imports

import (
	"strings"
	"testing"
)

func TestReadCSVHandlesBOMSemicolonsAndBlankLines(t *testing.T) {
	in := "\xef\xbb\xbfCompany;Email\nAcme;a@acme.test\n;\nBeta;b@beta.test\n"
	headers, recs, err := ReadCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if headers[0] != "Company" || len(recs) != 2 {
		t.Fatalf("headers %q recs %d", headers, len(recs))
	}
}

func TestReadCSVRejectsNonUTF8(t *testing.T) {
	if _, _, err := ReadCSV(strings.NewReader("name\n\xff\xfe\n")); err == nil {
		t.Fatal("want UTF-8 error")
	}
}

func TestSuggestMapping(t *testing.T) {
	m := SuggestMapping([]string{"Company Name", "Website URL", "E-mail", "First Name", "Industry", "Town", "Mystery", "company"})
	want := map[string]string{"0": "company_name", "1": "website", "2": "email", "3": "first_name", "4": "industry", "5": "city"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("column %s: got %q want %q", k, m[k], v)
		}
	}
	if _, ok := m["6"]; ok {
		t.Error("unknown header should be unmapped")
	}
	if _, ok := m["7"]; ok {
		t.Error("a field must not be mapped twice")
	}
}

func TestBuildRowsValidatesAndDedupes(t *testing.T) {
	recs := [][]string{
		{"Acme", "acme.test", "Jo@Acme.test", "Jo", "25", "2024"}, // ok
		{"", "nope.test", "x@nope.test", "", "", ""},              // missing company
		{"Bad Email Co", "", "not-an-email", "", "", ""},          // bad email
		{"Acme Again", "https://www.ACME.test/", "", "", "", ""},  // same domain as row 1
		{"Gamma", "", "jo@acme.test", "", "", ""},                 // same email as row 1
		{"Delta", "", "", "", "11-50", "opened 03/2025"},          // range + date parsing
		{"Existing Co", "existing.test", "", "", "", ""},          // matches DB
		{"Zeta", "", "", "", "lots", ""},                          // bad number
	}
	mapping := map[string]string{"0": "company_name", "1": "website", "2": "email", "3": "first_name", "4": "employee_count", "5": "founded_year"}
	rows := BuildRows(recs, mapping, "csv_import")
	Dedupe(rows, ExistingKeys{Domains: map[string]bool{"existing.test": true}, Emails: map[string]bool{}, Names: map[string]bool{}})

	if !rows[0].Valid() || rows[0].Duplicate != "" || rows[0].Contact.Email != "jo@acme.test" || rows[0].Domain != "acme.test" {
		t.Errorf("row 1: %+v", rows[0])
	}
	if rows[1].Valid() || rows[2].Valid() || rows[7].Valid() {
		t.Error("rows 2, 3 and 8 should be invalid")
	}
	if !strings.Contains(rows[3].Duplicate, "row 1") || !strings.Contains(rows[4].Duplicate, "row 1") {
		t.Errorf("in-file duplicates not detected: %q / %q", rows[3].Duplicate, rows[4].Duplicate)
	}
	if *rows[5].Lead.EmployeeCount != 11 || *rows[5].Lead.FoundedYear != 2025 {
		t.Errorf("row 6 parsing: %+v", rows[5].Lead)
	}
	if rows[6].Duplicate == "" {
		t.Error("row 7 should duplicate an existing lead")
	}
}
