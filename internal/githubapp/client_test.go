package githubapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testKey is a throwaway RSA key used only to exercise the JWT-signing code
// path in tests; it is not used against any real GitHub instance.
var testKey = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEpQIBAAKCAQEA0BUezcR7uycgZsfVLlAf4jXP7uFpVh4geSTY39RvYrAll0yh
q7uiQypP2hjQJ1eQXZvkAZx0v9lBYJmX7e0HiJckBr8+/O2kARL+GTCJDJZECpjy
97yylbzGBNl3s76fZ4CJ+4f11fCh7GJ3BJkMf9NFhe8g1TYS0BtSd/sauUQEuG/A
3fOJxKTNmICZr76xavOQ8agA4yW9V5hKcrbHzkfecg/sQsPMmrXixPNxMsqyOMmg
jdJ1aKr7ckEhd48ft4bPMO4DtVL/XFdK2wJZZ0gXJxWiT1Ny41LVql97Odm+OQyx
tcayMkGtMb1nwTcVVl+RG2U5E1lzOYpcQpyYFQIDAQABAoIBAAfUY55WgFlgdYWo
i0r81NZMNBDHBpGo/IvSaR6y/aX2/tMcnRC7NLXWR77rJBn234XGMeQloPb/E8iw
vtjDDH+FQGPImnQl9P/dWRZVjzKcDN9hNfNAdG/R9JmGHUz0JUddvNNsIEH2lgEx
C01u/Ntqdbk+cDvVlwuhm47MMgs6hJmZtS1KDPgYJu4IaB9oaZFN+pUyy8a1w0j9
RAhHpZrsulT5ThgCra4kKGDNnk2yfI91N9lkP5cnhgUmdZESDgrAJURLS8PgInM4
YPV9L68tJCO4g6k+hFiui4h/4cNXYkXnaZSBUoz28ICA6e7I3eJ6Y1ko4ou+Xf0V
csM8VFkCgYEA7y21JfECCfEsTHwwDg0fq2nld4o6FkIWAVQoIh6I6o6tYREmuZ/1
s81FPz/lvQpAvQUXGZlOPB9eW6bZZFytcuKYVNE/EVkuGQtpRXRT630CQiqvUYDZ
4FpqdBQUISt8KWpIofndrPSx6JzI80NSygShQsScWFw2wBIQAnV3TpsCgYEA3reL
L7AwlxCacsPvkazyYwyFfponblBX/OvrYUPPaEwGvSZmE5A/E4bdYTAixDdn4XvE
ChwpmRAWT/9C6jVJ/o1IK25dwnwg68gFDHlaOE+B5/9yNuDvVmg34PWngmpucFb/
6R/kIrF38lEfY0pRb05koW93uj1fj7Uiv+GWRw8CgYEAn1d3IIDQl+kJVydBKItL
tvoEur/m9N8wI9B6MEjhdEp7bXhssSvFF/VAFeQu3OMQwBy9B/vfaCSJy0t79uXb
U/dr/s2sU5VzJZI5nuDh67fLomMni4fpHxN9ajnaM0LyI/E/1FFPgqM+Rzb0lUQb
yqSM/ptXgXJls04VRl4VjtMCgYEAprO/bLx2QjxdPpXGFcXbz6OpsC92YC2nDlsP
3cfB0RFG4gGB2hbX/6eswHglLbVC/hWDkQWvZTATY2FvFps4fV4GrOt5Jn9+rL0U
elfC3e81Dw+2z7jhrE1ptepprUY4z8Fu33HNcuJfI3LxCYKxHZ0R2Xvzo+UYSBqO
ng0eTKUCgYEAxW9G4FjXQH0bjajntjoVQGLRVGWnteoOaQr/cy6oVii954yNMKSP
rezRkSNbJ8cqt9XQS+NNJ6Xwzl3EbuAt6r8f8VO1TIdRgFOgiUXRVNZ3ZyW8Hegd
kGTL0A6/0yAu9qQZlFbaD5bWhQo7eyx63u4hZGppBhkTSPikOYUPCH8=
-----END RSA PRIVATE KEY-----`)

func TestNew(t *testing.T) {
	if _, err := New(1, []byte("not a pem key")); err == nil {
		t.Fatal("expected error for invalid private key, got nil")
	}

	if _, err := New(1, testKey); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_CreateReview(t *testing.T) {
	const installationID = 42

	tests := []struct {
		name       string
		reviewResp int
		wantErr    bool
	}{
		{name: "success", reviewResp: http.StatusOK},
		{name: "github api error", reviewResp: http.StatusForbidden, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody struct {
				CommitID *string `json:"commit_id"`
				Body     *string `json:"body"`
				Event    *string `json:"event"`
				Comments []struct {
					Path *string `json:"path"`
					Line *int    `json:"line"`
					Body *string `json:"body"`
				} `json:"comments"`
			}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == fmt.Sprintf("/app/installations/%d/access_tokens", installationID):
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"token":      "test-token",
						"expires_at": time.Now().Add(5 * time.Minute),
					})
				case r.URL.Path == "/repos/octo-org/octo-repo/pulls/7/reviews":
					_ = json.NewDecoder(r.Body).Decode(&gotBody)
					w.WriteHeader(tt.reviewResp)
					_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
				default:
					t.Fatalf("unexpected request: %s", r.URL.Path)
				}
			}))
			defer ts.Close()

			client, err := New(1, testKey)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			client.baseURL = ts.URL

			comments := []InlineComment{{Path: "main.go", Line: 12, Body: "finding"}}
			err = client.CreateReview(context.Background(), installationID, "octo-org", "octo-repo", 7, "deadbeef", comments, "COMMENT", "summary")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if gotBody.CommitID == nil || *gotBody.CommitID != "deadbeef" {
				t.Fatalf("got commit_id %v, want deadbeef", gotBody.CommitID)
			}
			if gotBody.Event == nil || *gotBody.Event != "COMMENT" {
				t.Fatalf("got event %v, want COMMENT", gotBody.Event)
			}
			if gotBody.Body == nil || *gotBody.Body != "summary" {
				t.Fatalf("got body %v, want summary", gotBody.Body)
			}
			if len(gotBody.Comments) != 1 {
				t.Fatalf("got %d comments, want 1", len(gotBody.Comments))
			}
			c := gotBody.Comments[0]
			if c.Path == nil || *c.Path != "main.go" || c.Line == nil || *c.Line != 12 || c.Body == nil || *c.Body != "finding" {
				t.Fatalf("got comment %+v, want path=main.go line=12 body=finding", c)
			}
		})
	}
}

func TestClient_CommentOnPR(t *testing.T) {
	const installationID = 42

	tests := []struct {
		name        string
		commentResp int
		wantErr     bool
	}{
		{name: "success", commentResp: http.StatusCreated},
		{name: "github api error", commentResp: http.StatusForbidden, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == fmt.Sprintf("/app/installations/%d/access_tokens", installationID):
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"token":      "test-token",
						"expires_at": time.Now().Add(5 * time.Minute),
					})
				case r.URL.Path == "/repos/octo-org/octo-repo/issues/7/comments":
					var payload struct {
						Body string `json:"body"`
					}
					_ = json.NewDecoder(r.Body).Decode(&payload)
					gotBody = payload.Body
					w.WriteHeader(tt.commentResp)
					_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
				default:
					t.Fatalf("unexpected request: %s", r.URL.Path)
				}
			}))
			defer ts.Close()

			client, err := New(1, testKey)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			client.baseURL = ts.URL

			err = client.CommentOnPR(context.Background(), installationID, "octo-org", "octo-repo", 7, "hello PR")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotBody != "hello PR" {
				t.Fatalf("got comment body %q, want %q", gotBody, "hello PR")
			}
		})
	}
}
