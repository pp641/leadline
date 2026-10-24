package catalog

import (
	"context"
	"sort"
	"strings"
	"time"

	"leadline/internal/auth"
	"leadline/pkg/geo"
)

// Option is one dropdown choice and how many companies or leads have it.
type Option struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// CountryOption is a country; Count is 0 for countries with no data yet.
type CountryOption struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// PlaceOption is a state (City empty) or a city, with its parents so the UI
// can narrow states by country and cities by state. Country is a code, or ""
// when the data does not say.
type PlaceOption struct {
	Country string `json:"country"`
	State   string `json:"state"`
	Name    string `json:"name"`
	Count   int    `json:"count"`
}

// Facets lists the values present in the company database and the
// organization's leads, for the ICP Builder's dropdowns.
type Facets struct {
	Countries  []CountryOption `json:"countries"`
	States     []PlaceOption   `json:"states"`
	Cities     []PlaceOption   `json:"cities"`
	Industries []Option        `json:"industries"`
	TechStack  []Option        `json:"tech_stack"`
	// CompanyDB is false when the company database could not be read, so
	// only values from existing leads are listed.
	CompanyDB bool `json:"company_db"`
}

func (s *Service) Facets(ctx context.Context, p auth.Principal) (*Facets, error) {
	var all []FacetRows
	leadRows, err := queryFacetRows(ctx, s.pool, "leads", "industry", "organization_id = $1", []any{p.OrgID})
	if err != nil {
		return nil, err
	}
	all = append(all, leadRows)
	out := &Facets{}
	if f, ok := s.Source.(Faceter); ok {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		rows, err := f.FacetRows(cctx)
		cancel()
		if err == nil {
			all = append(all, rows)
			out.CompanyDB = true
		}
	}
	mergeFacets(out, all)
	return out, nil
}

// spellings tracks the most common spelling of a case-insensitive value.
type spellings struct {
	total int
	forms map[string]int
}

func (s *spellings) add(form string, n int) {
	if s.forms == nil {
		s.forms = map[string]int{}
	}
	s.total += n
	s.forms[form] += n
}

func (s *spellings) best() string {
	best, n := "", -1
	for f, c := range s.forms {
		if c > n || (c == n && f < best) {
			best, n = f, c
		}
	}
	return best
}

func mergeFacets(out *Facets, all []FacetRows) {
	countries := map[string]int{}
	states := map[[2]string]*spellings{}
	cities := map[[3]string]*spellings{}
	stateOf := map[[3]string]*spellings{} // display spelling of each city's state
	get := func(m map[[2]string]*spellings, k [2]string) *spellings {
		if m[k] == nil {
			m[k] = &spellings{}
		}
		return m[k]
	}
	industries, tech := map[string]int{}, map[string]int{}
	for _, r := range all {
		for _, pl := range r.Places {
			code := geo.Code(pl.Country)
			if code == "" && pl.Country != "" {
				code = pl.Country // unknown spelling: keep as is
			}
			if code != "" {
				countries[code] += pl.Count
			}
			stateKey := strings.ToLower(pl.State)
			if pl.State != "" {
				get(states, [2]string{code, stateKey}).add(pl.State, pl.Count)
			}
			if pl.City != "" {
				k := [3]string{code, stateKey, strings.ToLower(pl.City)}
				if cities[k] == nil {
					cities[k], stateOf[k] = &spellings{}, &spellings{}
				}
				cities[k].add(pl.City, pl.Count)
				stateOf[k].add(pl.State, pl.Count)
			}
		}
		for v, n := range r.Industries {
			industries[v] += n
		}
		for v, n := range r.TechStack {
			tech[v] += n
		}
	}

	for _, c := range geo.Countries() {
		out.Countries = append(out.Countries, CountryOption{Code: c.Code, Name: c.Name, Count: countries[c.Code]})
		delete(countries, c.Code)
	}
	for v, n := range countries { // spellings geo doesn't know
		out.Countries = append(out.Countries, CountryOption{Code: v, Name: v, Count: n})
	}
	sort.SliceStable(out.Countries, func(i, j int) bool {
		a, b := out.Countries[i], out.Countries[j]
		if (a.Count > 0) != (b.Count > 0) {
			return a.Count > 0
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Name < b.Name
	})

	out.States = []PlaceOption{}
	for k, s := range states {
		out.States = append(out.States, PlaceOption{Country: k[0], Name: s.best(), Count: s.total})
	}
	out.Cities = []PlaceOption{}
	for k, s := range cities {
		out.Cities = append(out.Cities, PlaceOption{Country: k[0], State: stateOf[k].best(), Name: s.best(), Count: s.total})
	}
	for _, l := range [][]PlaceOption{out.States, out.Cities} {
		sort.Slice(l, func(i, j int) bool {
			if l[i].Count != l[j].Count {
				return l[i].Count > l[j].Count
			}
			return l[i].Name < l[j].Name
		})
	}
	out.Industries, out.TechStack = options(industries), options(tech)
}

func options(m map[string]int) []Option {
	out := make([]Option, 0, len(m))
	for v, n := range m {
		out = append(out, Option{Value: v, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	return out
}
