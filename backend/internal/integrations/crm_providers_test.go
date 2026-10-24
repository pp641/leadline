package integrations

import "testing"

func TestAPIErrorText(t *testing.T) {
	for raw, want := range map[string]string{
		`{"errors":[{"id":"authentication_failed","code":401,"details":"No user found for the API key supplied"}]}`: "No user found for the API key supplied",
		`{"error":{"code":403,"message":"API key not valid","status":"PERMISSION_DENIED"}}`:                         "API key not valid",
		`[{"message":"Session expired or invalid","errorCode":"INVALID_SESSION_ID"}]`:                               "Session expired or invalid",
		`{"error":"invalid_client"}`: "invalid_client",
		`plain text`:                 "plain text",
	} {
		if got := apiErrorText([]byte(raw)); got != want {
			t.Errorf("%s → %q, want %q", raw, got, want)
		}
	}
}
