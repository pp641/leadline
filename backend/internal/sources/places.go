package sources

import (
	"context"
	"net/http"
	"strings"

	"leadline/internal/integrations"
)

// Places finds local businesses with Google's Places API (New) Text Search.
// A query returns at most 60 places (3 pages of 20).
type Places struct{ BaseURL string }

const placesFieldMask = "places.id,places.displayName,places.formattedAddress,places.addressComponents," +
	"places.nationalPhoneNumber,places.internationalPhoneNumber,places.websiteUri,places.primaryType," +
	"places.primaryTypeDisplayName,places.googleMapsUri,places.businessStatus,places.rating,places.userRatingCount,nextPageToken"

func (g *Places) base() string {
	if g.BaseURL != "" {
		return g.BaseURL
	}
	return "https://places.googleapis.com/v1"
}

func (g *Places) search(ctx context.Context, key, mask string, body map[string]any, out any) error {
	_, err := call(ctx, "google places", http.MethodPost, g.base()+"/places:searchText",
		map[string]string{"X-Goog-Api-Key": key, "X-Goog-FieldMask": mask}, body, out)
	return err
}

func (g *Places) Ping(ctx context.Context, c integrations.Credentials) error {
	return g.search(ctx, c.Get("api_key"), "places.id", map[string]any{"textQuery": "coffee in Delhi", "pageSize": 1}, nil)
}

func (g *Places) Search(ctx context.Context, c integrations.Credentials, p Params) ([]Candidate, error) {
	what, where := p.Str("query"), p.Str("location")
	if what == "" || where == "" {
		return nil, errorf("google places", "enter what to look for and where")
	}
	limit := p.Int("limit", 60, 60)
	body := map[string]any{"textQuery": what + " in " + where, "pageSize": 20}
	var cands []Candidate
	for page := 0; page < 3 && len(cands) < limit; page++ {
		var out obj
		if err := g.search(ctx, c.Get("api_key"), placesFieldMask, body, &out); err != nil {
			return nil, err
		}
		for _, pl := range out.arr("places") {
			if pl.s("businessStatus") == "CLOSED_PERMANENTLY" {
				continue
			}
			if p.Bool("only_with_website") && pl.s("websiteUri") == "" {
				continue
			}
			cands = append(cands, placeCandidate(pl))
		}
		next := out.s("nextPageToken")
		if next == "" {
			break
		}
		body["pageToken"] = next
	}
	if len(cands) > limit {
		cands = cands[:limit]
	}
	return cands, nil
}

func placeCandidate(pl obj) Candidate {
	c := Candidate{
		ExternalID: pl.s("id"), CompanyName: pl.s("displayName"), Website: pl.s("websiteUri"),
		Phone:   firstNonEmpty(pl.s("internationalPhoneNumber"), pl.s("nationalPhoneNumber")),
		Address: pl.s("formattedAddress"), SourceURL: pl.s("googleMapsUri"),
		Industry: strings.ToLower(firstNonEmpty(pl.s("primaryTypeDisplayName"), strings.ReplaceAll(pl.s("primaryType"), "_", " "))),
		Extra:    map[string]any{},
	}
	for _, comp := range pl.arr("addressComponents") {
		types := comp.strs("types")
		has := func(t string) bool {
			for _, x := range types {
				if x == t {
					return true
				}
			}
			return false
		}
		switch {
		case has("locality"):
			c.City = comp.s("longText")
		case has("administrative_area_level_1"):
			c.State = comp.s("longText")
		case has("country"):
			c.Country = comp.s("shortText", "longText")
		case has("sublocality_level_1") && c.City == "":
			c.City = comp.s("longText")
		}
	}
	if r := pl.path("rating"); r != nil {
		c.Extra["rating"] = r
		c.Extra["ratings"] = pl.path("userRatingCount")
	}
	return c
}
