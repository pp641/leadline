package tests

import (
	"regexp"
	"strings"
	"testing"
)

// TestOnboarding: a new account verifies its email with the emailed code
// (wrong codes are counted), the setup checklist reflects real progress, and
// an emailed invitation lets a second account join with the invited role.
func TestOnboarding(t *testing.T) {
	e := setup(t)
	e.app.Onboarding.SetMailer(e.provider)
	e.app.Auth.OnRegistered = nil // send explicitly below, so the test is deterministic
	owner := e.signup("onboard")

	var me struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	owner.must("GET", "/api/v1/auth/me", nil, &me, 200)
	if me.EmailVerified {
		t.Fatal("new account should start unverified")
	}
	owner.must("POST", "/api/v1/auth/verify-email/send", map[string]any{}, nil, 200)
	owner.must("POST", "/api/v1/auth/verify-email/send", map[string]any{}, nil, 409) // cooldown
	e.provider.mu.Lock()
	subject := e.provider.sent[len(e.provider.sent)-1].Subject
	e.provider.mu.Unlock()
	code := regexp.MustCompile(`\d{6}`).FindString(subject)
	if code == "" {
		t.Fatalf("no code in %q", subject)
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	owner.must("POST", "/api/v1/auth/verify-email", map[string]string{"code": wrong}, nil, 422)
	owner.must("POST", "/api/v1/auth/verify-email", map[string]string{"code": code}, nil, 200)
	owner.must("GET", "/api/v1/auth/me", nil, &me, 200)
	if !me.EmailVerified {
		t.Fatal("email should be verified")
	}

	var status struct {
		Checklist []struct {
			Key  string `json:"key"`
			Done bool   `json:"done"`
		} `json:"checklist"`
		State map[string]any `json:"state"`
	}
	owner.must("PUT", "/api/v1/onboarding", map[string]any{"step": "team", "profile": map[string]any{"industry": "agency"}}, &status, 200)
	if status.State["step"] != "team" {
		t.Fatalf("state not saved: %v", status.State)
	}
	done := map[string]bool{}
	for _, c := range status.Checklist {
		done[c.Key] = c.Done
	}
	if !done["verify_email"] || done["team"] || done["campaign"] {
		t.Fatalf("checklist: %v", done)
	}

	// Invite a teammate; the link in the email carries the token.
	invitee := "teammate-" + strings.ToLower(t.Name()) + "@users.test"
	var res struct {
		Invited []string `json:"invited"`
	}
	owner.must("POST", "/api/v1/invitations", map[string]any{"emails": []string{invitee, "not-an-email"}, "role": "viewer"}, &res, 200)
	if len(res.Invited) != 1 {
		t.Fatalf("invite result %+v", res)
	}
	e.provider.mu.Lock()
	body := e.provider.sent[len(e.provider.sent)-1].TextBody
	e.provider.mu.Unlock()
	token := regexp.MustCompile(`token=([A-Za-z0-9_-]+)`).FindStringSubmatch(body)
	if token == nil {
		t.Fatalf("no invite link in %q", body)
	}
	var info struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	anon := &client{e: e}
	anon.must("GET", "/api/v1/invitations/lookup?token="+token[1], nil, &info, 200)
	if info.Email != invitee || info.Role != "viewer" {
		t.Fatalf("lookup %+v", info)
	}

	// Someone else can't use it; the invited address can.
	stranger := e.signup("stranger")
	stranger.must("POST", "/api/v1/invitations/accept", map[string]string{"token": token[1]}, nil, 403)
	mate := &client{e: e}
	var authRes struct {
		AccessToken string `json:"access_token"`
	}
	mate.must("POST", "/api/v1/auth/register", map[string]any{"email": invitee, "password": "correct-horse-battery", "full_name": "Mate", "accept_terms": true}, &authRes, 201)
	mate.token = authRes.AccessToken
	mate.must("POST", "/api/v1/invitations/accept", map[string]string{"token": token[1]}, nil, 200)
	mate.must("POST", "/api/v1/invitations/accept", map[string]string{"token": token[1]}, nil, 409) // single use
	var orgs []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}
	mate.must("GET", "/api/v1/organizations", nil, &orgs, 200)
	if len(orgs) != 1 || orgs[0].ID != owner.org || orgs[0].Role != "viewer" {
		t.Fatalf("membership %+v (want org %s as viewer)", orgs, owner.org)
	}
	mate.must("GET", "/api/v1/auth/me", nil, &me, 200)
	if !me.EmailVerified {
		t.Fatal("accepting an emailed invitation should verify the address")
	}

	// Sign-up requires accepting the terms.
	(&client{e: e}).must("POST", "/api/v1/auth/register", map[string]any{"email": "noterms@users.test", "password": "correct-horse-battery", "full_name": "N"}, nil, 422)
}
