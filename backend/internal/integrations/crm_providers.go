package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"leadline/pkg/safehttp"
)

// CRMCompany and CRMPerson are what Leadline pushes to a CRM.
type CRMCompany struct {
	Name, Domain, Website, Phone, City, State, Country, Description string
	Employees, Founded                                              *int
}

type CRMPerson struct {
	Email, FirstName, LastName, Title, Company string
}

// CRM is implemented by every CRM integration. Upserts take the id stored from
// a previous sync ("" for none) and return the CRM's id.
type CRM interface {
	Name() string
	Ping(ctx context.Context) error
	UpsertCompany(ctx context.Context, id string, c CRMCompany) (string, error)
	UpsertPerson(ctx context.Context, id, companyID string, p CRMPerson) (string, error)
}

// CRM returns a client for a connected CRM integration.
func (r *Registry) CRM(ctx context.Context, orgID uuid.UUID, kind string) (CRM, error) {
	creds, err := r.Credentials(ctx, orgID, kind)
	if err != nil {
		return nil, err
	}
	if kind == KindHubSpot {
		return &hubspotCRM{&HubSpot{Token: creds.Get("token"), BaseURL: r.BaseURLs[KindHubSpot]}}, nil
	}
	return r.crmFor(kind, creds)
}

func (r *Registry) crmFor(kind string, c Credentials) (CRM, error) {
	switch kind {
	case KindHubSpot:
		return &hubspotCRM{&HubSpot{Token: c.Get("token"), BaseURL: r.BaseURLs[KindHubSpot]}}, nil
	case KindPipedrive:
		return &Pipedrive{Token: c.Get("api_token"), BaseURL: r.BaseURLs[KindPipedrive]}, nil
	case KindSalesforce:
		return &Salesforce{InstanceURL: strings.TrimRight(c.Get("instance_url"), "/"), ClientID: c.Get("client_id"), ClientSecret: c.Get("client_secret")}, nil
	case KindZoho:
		return &Zoho{DataCenter: c.Get("data_center"), ClientID: c.Get("client_id"), ClientSecret: c.Get("client_secret"), RefreshToken: c.Get("refresh_token")}, nil
	}
	return nil, fmt.Errorf("%s is not a CRM", kind)
}

// ───────────── shared HTTP ─────────────

// apiClient refuses private and metadata addresses: some base URLs (e.g. a
// Salesforce My Domain) are user-supplied.
var apiClient = safehttp.NewClient(25 * time.Second)

// CallJSON sends a JSON (or form, when form != nil) request and decodes a JSON response.
func CallJSON(ctx context.Context, name, method, u string, headers map[string]string, body any, form url.Values, out any) (int, error) {
	var rdr io.Reader
	ctype := ""
	switch {
	case form != nil:
		rdr, ctype = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
	case body != nil:
		b, _ := json.Marshal(body)
		rdr, ctype = bytes.NewReader(b), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return 0, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s: HTTP %d: %s", name, resp.StatusCode, apiErrorText(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s: unexpected response: %w", name, err)
		}
	}
	return resp.StatusCode, nil
}

func lastNameOr(p CRMPerson) string {
	if p.LastName != "" {
		return p.LastName
	}
	if p.FirstName != "" {
		return p.FirstName
	}
	if at := strings.Index(p.Email, "@"); at > 0 {
		return p.Email[:at]
	}
	return "Unknown"
}

// ───────────── HubSpot adapter ─────────────

type hubspotCRM struct{ h *HubSpot }

func (c *hubspotCRM) Name() string                   { return "HubSpot" }
func (c *hubspotCRM) Ping(ctx context.Context) error { return c.h.Ping(ctx) }

func (c *hubspotCRM) UpsertCompany(ctx context.Context, id string, co CRMCompany) (string, error) {
	props := map[string]string{"name": co.Name, "city": co.City, "state": co.State, "country": co.Country, "phone": co.Phone, "description": co.Description}
	if co.Domain != "" {
		props["domain"] = co.Domain
	}
	if co.Website != "" {
		props["website"] = co.Website
	}
	if co.Employees != nil {
		props["numberofemployees"] = strconv.Itoa(*co.Employees)
	}
	if co.Founded != nil {
		props["founded_year"] = strconv.Itoa(*co.Founded)
	}
	return c.h.UpsertCompany(ctx, id, props)
}

func (c *hubspotCRM) UpsertPerson(ctx context.Context, id, companyID string, p CRMPerson) (string, error) {
	contactID, err := c.h.UpsertContact(ctx, id, p.Email, map[string]string{
		"email": p.Email, "firstname": p.FirstName, "lastname": p.LastName, "jobtitle": p.Title, "company": p.Company,
	})
	if err != nil || companyID == "" {
		return contactID, err
	}
	return contactID, c.h.Associate(ctx, contactID, companyID)
}

// ───────────── Pipedrive ─────────────

// Pipedrive uses the v1 REST API with a personal API token.
type Pipedrive struct {
	Token   string
	BaseURL string
}

func (p *Pipedrive) Name() string { return "Pipedrive" }

func (p *Pipedrive) url(path string, q url.Values) string {
	base := p.BaseURL
	if base == "" {
		base = "https://api.pipedrive.com/v1"
	}
	if q == nil {
		q = url.Values{}
	}
	q.Set("api_token", p.Token)
	return base + path + "?" + q.Encode()
}

type pdResp struct {
	Success bool `json:"success"`
	Data    struct {
		ID    int `json:"id"`
		Items []struct {
			Item struct {
				ID int `json:"id"`
			} `json:"item"`
		} `json:"items"`
	} `json:"data"`
}

func (p *Pipedrive) Ping(ctx context.Context) error {
	_, err := CallJSON(ctx, "pipedrive", http.MethodGet, p.url("/users/me", nil), nil, nil, nil, nil)
	return err
}

func (p *Pipedrive) UpsertCompany(ctx context.Context, id string, c CRMCompany) (string, error) {
	addr := strings.Join(nonEmpty(c.City, c.State, c.Country), ", ")
	body := map[string]any{"name": c.Name}
	if addr != "" {
		body["address"] = addr
	}
	var out pdResp
	if id != "" {
		status, err := CallJSON(ctx, "pipedrive", http.MethodPut, p.url("/organizations/"+id, nil), nil, body, nil, &out)
		if status != http.StatusNotFound && status != http.StatusGone {
			return id, err
		}
	}
	_, err := CallJSON(ctx, "pipedrive", http.MethodPost, p.url("/organizations", nil), nil, body, nil, &out)
	return strconv.Itoa(out.Data.ID), err
}

func (p *Pipedrive) UpsertPerson(ctx context.Context, id, companyID string, person CRMPerson) (string, error) {
	if id == "" && person.Email != "" {
		var found pdResp
		if _, err := CallJSON(ctx, "pipedrive", http.MethodGet, p.url("/persons/search", url.Values{
			"term": {person.Email}, "fields": {"email"}, "exact_match": {"true"}, "limit": {"1"}}), nil, nil, nil, &found); err != nil {
			return "", err
		}
		if len(found.Data.Items) > 0 {
			id = strconv.Itoa(found.Data.Items[0].Item.ID)
		}
	}
	name := strings.TrimSpace(person.FirstName + " " + person.LastName)
	if name == "" {
		name = lastNameOr(person)
	}
	body := map[string]any{"name": name}
	if person.Email != "" {
		body["email"] = []map[string]any{{"value": person.Email, "primary": true, "label": "work"}}
	}
	if org, err := strconv.Atoi(companyID); err == nil && org > 0 {
		body["org_id"] = org
	}
	if person.Title != "" {
		body["job_title"] = person.Title
	}
	var out pdResp
	if id != "" {
		status, err := CallJSON(ctx, "pipedrive", http.MethodPut, p.url("/persons/"+id, nil), nil, body, nil, &out)
		if status != http.StatusNotFound && status != http.StatusGone {
			return id, err
		}
	}
	_, err := CallJSON(ctx, "pipedrive", http.MethodPost, p.url("/persons", nil), nil, body, nil, &out)
	return strconv.Itoa(out.Data.ID), err
}

// ───────────── Salesforce ─────────────

// Salesforce authenticates with the OAuth 2.0 client credentials flow of a
// connected app and uses the REST sObject API.
type Salesforce struct {
	InstanceURL, ClientID, ClientSecret string

	mu       sync.Mutex
	token    string
	apiBase  string
	expireAt time.Time
}

const sfVersion = "v60.0"

func (s *Salesforce) Name() string { return "Salesforce" }

func (s *Salesforce) auth(ctx context.Context) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Before(s.expireAt) {
		return s.token, s.apiBase, nil
	}
	var out struct {
		AccessToken string `json:"access_token"`
		InstanceURL string `json:"instance_url"`
	}
	_, err := CallJSON(ctx, "salesforce auth", http.MethodPost, s.InstanceURL+"/services/oauth2/token", nil, nil, url.Values{
		"grant_type": {"client_credentials"}, "client_id": {s.ClientID}, "client_secret": {s.ClientSecret}}, &out)
	if err != nil {
		return "", "", err
	}
	s.token, s.apiBase, s.expireAt = out.AccessToken, out.InstanceURL, time.Now().Add(50*time.Minute)
	if s.apiBase == "" {
		s.apiBase = s.InstanceURL
	}
	return s.token, s.apiBase, nil
}

func (s *Salesforce) do(ctx context.Context, method, path string, body, out any) (int, error) {
	tok, base, err := s.auth(ctx)
	if err != nil {
		return 0, err
	}
	return CallJSON(ctx, "salesforce", method, base+"/services/data/"+sfVersion+path, map[string]string{"Authorization": "Bearer " + tok}, body, nil, out)
}

func (s *Salesforce) Ping(ctx context.Context) error {
	_, err := s.do(ctx, http.MethodGet, "/limits", nil, nil)
	return err
}

func (s *Salesforce) upsert(ctx context.Context, object, id string, fields map[string]any) (string, error) {
	if id != "" {
		status, err := s.do(ctx, http.MethodPatch, "/sobjects/"+object+"/"+id, fields, nil)
		if status != http.StatusNotFound {
			return id, err
		}
	}
	var out struct {
		ID string `json:"id"`
	}
	_, err := s.do(ctx, http.MethodPost, "/sobjects/"+object, fields, &out)
	return out.ID, err
}

func (s *Salesforce) UpsertCompany(ctx context.Context, id string, c CRMCompany) (string, error) {
	f := map[string]any{"Name": c.Name, "Website": c.Website, "Phone": c.Phone, "BillingCity": c.City,
		"BillingState": c.State, "BillingCountry": c.Country, "Description": truncateStr(c.Description, 32000)}
	if c.Employees != nil {
		f["NumberOfEmployees"] = *c.Employees
	}
	return s.upsert(ctx, "Account", id, f)
}

func (s *Salesforce) UpsertPerson(ctx context.Context, id, companyID string, p CRMPerson) (string, error) {
	if id == "" && p.Email != "" {
		var found struct {
			Records []struct {
				ID string `json:"Id"`
			} `json:"records"`
		}
		q := "SELECT Id FROM Contact WHERE Email = '" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(p.Email) + "' LIMIT 1"
		if _, err := s.do(ctx, http.MethodGet, "/query?q="+url.QueryEscape(q), nil, &found); err != nil {
			return "", err
		}
		if len(found.Records) > 0 {
			id = found.Records[0].ID
		}
	}
	f := map[string]any{"FirstName": p.FirstName, "LastName": lastNameOr(p), "Email": p.Email, "Title": p.Title}
	if companyID != "" {
		f["AccountId"] = companyID
	}
	return s.upsert(ctx, "Contact", id, f)
}

// ───────────── Zoho CRM ─────────────

// Zoho uses a self-client refresh token to mint access tokens (v6 REST API).
type Zoho struct {
	DataCenter, ClientID, ClientSecret, RefreshToken string

	mu       sync.Mutex
	token    string
	apiBase  string
	expireAt time.Time
}

func (z *Zoho) Name() string { return "Zoho CRM" }

func (z *Zoho) accountsHost() string {
	switch z.DataCenter {
	case "ca":
		return "https://accounts.zohocloud.ca"
	case "", "com":
		return "https://accounts.zoho.com"
	default:
		return "https://accounts.zoho." + z.DataCenter
	}
}

func (z *Zoho) auth(ctx context.Context) (string, string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.token != "" && time.Now().Before(z.expireAt) {
		return z.token, z.apiBase, nil
	}
	var out struct {
		AccessToken string `json:"access_token"`
		APIDomain   string `json:"api_domain"`
		Error       string `json:"error"`
	}
	_, err := CallJSON(ctx, "zoho auth", http.MethodPost, z.accountsHost()+"/oauth/v2/token", nil, nil, url.Values{
		"refresh_token": {z.RefreshToken}, "client_id": {z.ClientID}, "client_secret": {z.ClientSecret}, "grant_type": {"refresh_token"}}, &out)
	if err != nil {
		return "", "", err
	}
	if out.AccessToken == "" {
		return "", "", fmt.Errorf("zoho auth: %s", firstNonEmpty(out.Error, "no access token returned"))
	}
	z.token, z.apiBase, z.expireAt = out.AccessToken, out.APIDomain, time.Now().Add(50*time.Minute)
	if z.apiBase == "" {
		z.apiBase = strings.Replace(z.accountsHost(), "accounts.", "www.", 1)
		z.apiBase = strings.Replace(z.apiBase, "zoho.", "zohoapis.", 1)
	}
	return z.token, z.apiBase, nil
}

func (z *Zoho) do(ctx context.Context, method, path string, body, out any) (int, error) {
	tok, base, err := z.auth(ctx)
	if err != nil {
		return 0, err
	}
	return CallJSON(ctx, "zoho", method, base+"/crm/v6"+path, map[string]string{"Authorization": "Zoho-oauthtoken " + tok}, body, nil, out)
}

func (z *Zoho) Ping(ctx context.Context) error {
	_, err := z.do(ctx, http.MethodGet, "/users?type=CurrentUser", nil, nil)
	return err
}

type zohoWrite struct {
	Data []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			ID string `json:"id"`
		} `json:"details"`
	} `json:"data"`
}

func (z *Zoho) write(ctx context.Context, module, id string, rec map[string]any) (string, error) {
	var out zohoWrite
	method, path := http.MethodPost, "/"+module
	if id != "" {
		method, path = http.MethodPut, "/"+module+"/"+id
	}
	status, err := z.do(ctx, method, path, map[string]any{"data": []any{rec}}, &out)
	if id != "" && status == http.StatusNotFound {
		return z.write(ctx, module, "", rec)
	}
	if err != nil {
		return "", err
	}
	if len(out.Data) == 0 || (out.Data[0].Code != "SUCCESS" && out.Data[0].Details.ID == "") {
		msg := "no record returned"
		if len(out.Data) > 0 {
			msg = out.Data[0].Code + ": " + out.Data[0].Message
		}
		return "", fmt.Errorf("zoho %s: %s", module, msg)
	}
	return out.Data[0].Details.ID, nil
}

func (z *Zoho) UpsertCompany(ctx context.Context, id string, c CRMCompany) (string, error) {
	rec := map[string]any{"Account_Name": c.Name, "Website": c.Website, "Phone": c.Phone, "Billing_City": c.City,
		"Billing_State": c.State, "Billing_Country": c.Country, "Description": c.Description}
	if c.Employees != nil {
		rec["Employees"] = *c.Employees
	}
	return z.write(ctx, "Accounts", id, rec)
}

func (z *Zoho) UpsertPerson(ctx context.Context, id, companyID string, p CRMPerson) (string, error) {
	if id == "" && p.Email != "" {
		var found struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if _, err := z.do(ctx, http.MethodGet, "/Contacts/search?email="+url.QueryEscape(p.Email), nil, &found); err != nil {
			return "", err
		}
		if len(found.Data) > 0 {
			id = found.Data[0].ID
		}
	}
	rec := map[string]any{"First_Name": p.FirstName, "Last_Name": lastNameOr(p), "Email": p.Email, "Title": p.Title}
	if companyID != "" {
		rec["Account_Name"] = map[string]string{"id": companyID}
	}
	return z.write(ctx, "Contacts", id, rec)
}

// ───────────── helpers ─────────────

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// SetHTTPClientForTest swaps the provider HTTP client and returns the old one
// (tests serve fake APIs on localhost, which the guarded client refuses).
func SetHTTPClientForTest(c *http.Client) *http.Client {
	old := apiClient
	apiClient = c
	return old
}

// apiErrorText pulls a readable message out of a provider's error body.
func apiErrorText(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) == nil {
		if msg := findMessage(v, 0); msg != "" {
			return truncateStr(msg, 300)
		}
	}
	return truncateStr(strings.TrimSpace(string(raw)), 300)
}

func findMessage(v any, depth int) string {
	if depth > 4 {
		return ""
	}
	switch x := v.(type) {
	case map[string]any:
		for _, k := range []string{"details", "message", "error_description", "detail", "error", "errors", "title"} {
			switch inner := x[k].(type) {
			case string:
				if strings.TrimSpace(inner) != "" {
					return strings.TrimSpace(inner)
				}
			case map[string]any, []any:
				if m := findMessage(inner, depth+1); m != "" {
					return m
				}
			}
		}
	case []any:
		for _, item := range x {
			if m := findMessage(item, depth+1); m != "" {
				return m
			}
		}
	}
	return ""
}
