package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HubSpot pushes companies and contacts through the CRM v3/v4 APIs using a
// private app access token (scopes: crm.objects.companies.write,
// crm.objects.contacts.write and their read scopes).
type HubSpot struct {
	Token   string
	BaseURL string
}

var hubspotClient = &http.Client{Timeout: 20 * time.Second}

func (h *HubSpot) base() string {
	if h.BaseURL != "" {
		return h.BaseURL
	}
	return "https://api.hubapi.com"
}

func (h *HubSpot) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base()+path, rdr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hubspotClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("hubspot %s %s: HTTP %d: %s", method, path, resp.StatusCode, truncateStr(string(raw), 300))
	}
	if out != nil && len(raw) > 0 {
		return resp.StatusCode, json.Unmarshal(raw, out)
	}
	return resp.StatusCode, nil
}

func (h *HubSpot) Ping(ctx context.Context) error {
	_, err := h.do(ctx, http.MethodGet, "/crm/v3/objects/companies?limit=1", nil, nil)
	return err
}

// UpsertCompany creates or (when id is set) updates a company; it returns the id.
func (h *HubSpot) UpsertCompany(ctx context.Context, id string, props map[string]string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if id != "" {
		status, err := h.do(ctx, http.MethodPatch, "/crm/v3/objects/companies/"+id, map[string]any{"properties": props}, &out)
		if status != http.StatusNotFound {
			return id, err
		}
		// Deleted in HubSpot: fall through and recreate.
	}
	_, err := h.do(ctx, http.MethodPost, "/crm/v3/objects/companies", map[string]any{"properties": props}, &out)
	return out.ID, err
}

// UpsertContact finds a contact by email (or uses id) and creates/updates it.
func (h *HubSpot) UpsertContact(ctx context.Context, id, email string, props map[string]string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if id == "" && email != "" {
		var found struct {
			Results []struct {
				ID string `json:"id"`
			} `json:"results"`
		}
		_, err := h.do(ctx, http.MethodPost, "/crm/v3/objects/contacts/search", map[string]any{
			"filterGroups": []any{map[string]any{"filters": []any{map[string]string{"propertyName": "email", "operator": "EQ", "value": email}}}},
			"limit":        1,
		}, &found)
		if err != nil {
			return "", err
		}
		if len(found.Results) > 0 {
			id = found.Results[0].ID
		}
	}
	if id != "" {
		status, err := h.do(ctx, http.MethodPatch, "/crm/v3/objects/contacts/"+id, map[string]any{"properties": props}, &out)
		if status != http.StatusNotFound {
			return id, err
		}
	}
	_, err := h.do(ctx, http.MethodPost, "/crm/v3/objects/contacts", map[string]any{"properties": props}, &out)
	return out.ID, err
}

// Associate links a contact to a company with the default association type.
func (h *HubSpot) Associate(ctx context.Context, contactID, companyID string) error {
	_, err := h.do(ctx, http.MethodPut, "/crm/v4/objects/contact/"+contactID+"/associations/default/company/"+companyID, nil, nil)
	return err
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
