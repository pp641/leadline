// Package imports implements the CSV import pipeline:
//
//	Upload → Parse → Validate → Deduplicate → Preview → (commit) → Import
//
// Parsing, validation and dedupe are pure functions over the file so the
// preview the user approves is exactly what the background import writes.
package imports

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"leadline/internal/contacts"
	"leadline/internal/leads"
	"leadline/pkg/apperr"
)

const MaxRows = 50000

// Fields that a CSV column can be mapped to.
var Fields = []string{
	"company_name", "website", "email", "first_name", "last_name", "job_title", "phone", "industry",
	"description", "country", "state", "city", "address", "linkedin_url", "employee_count", "founded_year",
	"business_email", "tags", "source_url",
}

var aliases = map[string]string{
	"company": "company_name", "company name": "company_name", "business": "company_name", "business name": "company_name",
	"organization": "company_name", "organisation": "company_name", "name": "company_name", "account": "company_name",
	"url": "website", "domain": "website", "web": "website", "site": "website", "website url": "website",
	"email address": "email", "e mail": "email", "e mail address": "email", "mail": "email", "contact email": "email", "work email": "email",
	"first": "first_name", "firstname": "first_name", "first name": "first_name", "given name": "first_name",
	"last": "last_name", "lastname": "last_name", "last name": "last_name", "surname": "last_name",
	"title": "job_title", "job title": "job_title", "position": "job_title", "role": "job_title", "designation": "job_title",
	"phone number": "phone", "telephone": "phone", "mobile": "phone", "tel": "phone",
	"sector": "industry", "category": "industry", "vertical": "industry",
	"about": "description", "summary": "description",
	"region": "state", "province": "state", "town": "city", "location": "city",
	"street": "address", "street address": "address",
	"linkedin": "linkedin_url", "linkedin url": "linkedin_url", "linkedin profile": "linkedin_url",
	"employees": "employee_count", "employee count": "employee_count", "headcount": "employee_count", "company size": "employee_count",
	"founded": "founded_year", "year founded": "founded_year", "founded year": "founded_year", "opened": "founded_year",
	"generic email": "business_email", "company email": "business_email", "business email": "business_email",
	"tag": "tags", "labels": "tags", "source": "source_url",
}

var headerClean = regexp.MustCompile(`[_\-\s]+`)

// SuggestMapping maps CSV headers to fields by name or alias.
// The result is header index → field.
func SuggestMapping(headers []string) map[string]string {
	out := map[string]string{}
	used := map[string]bool{}
	for i, h := range headers {
		key := strings.ToLower(strings.TrimSpace(headerClean.ReplaceAllString(h, " ")))
		field := ""
		if f := strings.ReplaceAll(key, " ", "_"); contains(Fields, f) {
			field = f
		} else if f, ok := aliases[key]; ok {
			field = f
		}
		if field != "" && !used[field] {
			used[field] = true
			out[strconv.Itoa(i)] = field
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Row is one validated CSV row.
type Row struct {
	Number    int               `json:"row"` // 1-based, excluding header
	Values    map[string]string `json:"values"`
	Errors    []string          `json:"errors,omitempty"`
	Duplicate string            `json:"duplicate,omitempty"` // reason, when a duplicate

	Lead    *leads.Input    `json:"-"`
	Domain  string          `json:"-"`
	Contact *contacts.Input `json:"-"`
}

func (r Row) Valid() bool { return len(r.Errors) == 0 }

// ReadCSV reads headers and records, tolerating a UTF-8 BOM, ragged rows and
// semicolon-delimited files.
func ReadCSV(r io.Reader) ([]string, [][]string, error) {
	data, err := io.ReadAll(io.LimitReader(r, 50<<20))
	if err != nil {
		return nil, nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		return nil, nil, errors.New("file is not valid UTF-8; please export it as CSV UTF-8")
	}
	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true
	if firstLine, _, _ := strings.Cut(string(data), "\n"); strings.Count(firstLine, ";") > strings.Count(firstLine, ",") {
		cr.Comma = ';'
	}
	headers, err := cr.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, errors.New("file is empty")
		}
		return nil, nil, fmt.Errorf("could not read header row: %w", err)
	}
	var records [][]string
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("line %d: %w", len(records)+2, err)
		}
		if isBlank(rec) {
			continue
		}
		if len(records) >= MaxRows {
			return nil, nil, fmt.Errorf("file has more than %d rows; split it into smaller files", MaxRows)
		}
		records = append(records, rec)
	}
	return headers, records, nil
}

func isBlank(rec []string) bool {
	for _, v := range rec {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// BuildRows applies the mapping and validates each record.
func BuildRows(records [][]string, mapping map[string]string, source string) []Row {
	rows := make([]Row, 0, len(records))
	for i, rec := range records {
		vals := map[string]string{}
		for idx, field := range mapping {
			n, err := strconv.Atoi(idx)
			if err != nil || n < 0 || n >= len(rec) || field == "" {
				continue
			}
			if v := strings.TrimSpace(rec[n]); v != "" {
				vals[field] = v
			}
		}
		rows = append(rows, buildRow(i+1, vals, source))
	}
	return rows
}

func buildRow(num int, vals map[string]string, source string) Row {
	row := Row{Number: num, Values: vals}
	in := &leads.Input{
		CompanyName: vals["company_name"], Website: vals["website"], Industry: vals["industry"],
		Description: vals["description"], Country: vals["country"], State: vals["state"], City: vals["city"],
		Address: vals["address"], Phone: vals["phone"], BusinessEmail: vals["business_email"],
		LinkedInURL: vals["linkedin_url"], SourceURL: vals["source_url"], Source: source,
	}
	if t := vals["tags"]; t != "" {
		in.Tags = strings.FieldsFunc(t, func(r rune) bool { return r == ';' || r == ',' || r == '|' })
	}
	if v := vals["employee_count"]; v != "" {
		if n, ok := parseInt(v); ok {
			in.EmployeeCount = &n
		} else {
			row.Errors = append(row.Errors, "employee_count: not a number")
		}
	}
	if v := vals["founded_year"]; v != "" {
		if n, ok := parseYear(v); ok {
			in.FoundedYear = &n
		} else {
			row.Errors = append(row.Errors, "founded_year: not a year")
		}
	}
	domain, err := leads.Prepare(in)
	row.Errors = append(row.Errors, fieldErrors(err)...)
	row.Lead, row.Domain = in, domain

	if vals["email"] != "" || vals["first_name"] != "" || vals["last_name"] != "" {
		ci := &contacts.Input{
			FirstName: vals["first_name"], LastName: vals["last_name"], JobTitle: vals["job_title"],
			Email: vals["email"], Source: source, IsPrimary: true,
		}
		row.Errors = append(row.Errors, fieldErrors(ci.Prepare())...)
		row.Contact = ci
	}
	return row
}

// parseInt accepts "25", "1,200" and ranges like "11-50" (uses the lower bound).
func parseInt(s string) (int, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	s = strings.TrimSuffix(s, "+")
	if lo, _, ok := strings.Cut(s, "-"); ok {
		s = strings.TrimSpace(lo)
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0
}

var yearRe = regexp.MustCompile(`\b(1[89]\d\d|20\d\d)\b`)

// parseYear accepts "2024" or a date such as "2024-03-01" / "03/01/2024".
func parseYear(s string) (int, bool) {
	m := yearRe.FindString(s)
	if m == "" {
		return 0, false
	}
	n, _ := strconv.Atoi(m)
	return n, true
}

func fieldErrors(err error) []string {
	if err == nil {
		return nil
	}
	e, ok := apperr.As(err)
	if !ok || len(e.Fields) == 0 {
		return []string{err.Error()}
	}
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+": "+e.Fields[k])
	}
	return out
}

// Dedupe marks rows that duplicate an earlier row in the file or an existing
// lead. A row is a duplicate when its website domain, contact email, or
// company name + city matches.
func Dedupe(rows []Row, existing ExistingKeys) {
	seenDomain, seenEmail, seenName := map[string]int{}, map[string]int{}, map[string]int{}
	for i := range rows {
		r := &rows[i]
		if !r.Valid() {
			continue
		}
		email := ""
		if r.Contact != nil {
			email = r.Contact.Email
		}
		name := leads.NameKey(r.Lead.CompanyName, r.Lead.City)
		switch {
		case r.Domain != "" && existing.Domains[r.Domain]:
			r.Duplicate = "website matches an existing lead"
		case email != "" && existing.Emails[email]:
			r.Duplicate = "email matches an existing contact"
		case name != "" && existing.Names[name]:
			r.Duplicate = "company name and city match an existing lead"
		case r.Domain != "" && seenDomain[r.Domain] > 0:
			r.Duplicate = fmt.Sprintf("same website as row %d", seenDomain[r.Domain])
		case email != "" && seenEmail[email] > 0:
			r.Duplicate = fmt.Sprintf("same email as row %d", seenEmail[email])
		case name != "" && seenName[name] > 0:
			r.Duplicate = fmt.Sprintf("same company and city as row %d", seenName[name])
		}
		if r.Duplicate != "" {
			continue
		}
		if r.Domain != "" {
			seenDomain[r.Domain] = r.Number
		}
		if email != "" {
			seenEmail[email] = r.Number
		}
		if name != "" {
			seenName[name] = r.Number
		}
	}
}

type ExistingKeys struct {
	Domains map[string]bool
	Emails  map[string]bool
	Names   map[string]bool
}

// Keys collects the dedupe keys present in rows so only those are looked up.
func Keys(rows []Row) (domains, emails, names []string) {
	for _, r := range rows {
		if !r.Valid() {
			continue
		}
		if r.Domain != "" {
			domains = append(domains, r.Domain)
		}
		if r.Contact != nil && r.Contact.Email != "" {
			emails = append(emails, r.Contact.Email)
		}
		if n := leads.NameKey(r.Lead.CompanyName, r.Lead.City); n != "" {
			names = append(names, n)
		}
	}
	return
}
