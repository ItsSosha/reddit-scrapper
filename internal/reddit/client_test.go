package reddit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleListing = `{"data":{"after":null,"children":[
{"data":{"id":"abc123","subreddit":"fontainesdc","title":"Spare ticket for Berlin &amp; Hamburg","selftext":"DM me","link_flair_text":"Tickets","author":"someone","url":"https://redd.it/abc123","permalink":"/r/fontainesdc/comments/abc123/spare/","created_utc":1700000000,"over_18":false}},
{"data":{"id":"def456","subreddit":"fontainesdc","title":"Setlist thread","selftext":"","author":"other","permalink":"/r/fontainesdc/comments/def456/setlist/","created_utc":1700000100}}
]}}`

func TestListingUnauthenticated(t *testing.T) {
	var gotPath, gotQuery, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotUA = r.URL.Path, r.URL.RawQuery, r.Header.Get("User-Agent")
		fmt.Fprint(w, sampleListing)
	}))
	defer srv.Close()

	c := New(Options{UserAgent: "test-agent"})
	c.publicBase = srv.URL

	posts, err := c.Listing(context.Background(), "fontainesdc", "new", 25)
	if err != nil {
		t.Fatalf("Listing: %v", err)
	}
	if gotPath != "/r/fontainesdc/new.json" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "limit=25") {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotUA != "test-agent" {
		t.Fatalf("user agent = %q", gotUA)
	}
	if len(posts) != 2 {
		t.Fatalf("got %d posts, want 2", len(posts))
	}
	if posts[0].Title != "Spare ticket for Berlin & Hamburg" {
		t.Fatalf("title not unescaped: %q", posts[0].Title)
	}
	if posts[0].Permalink != "https://www.reddit.com/r/fontainesdc/comments/abc123/spare/" {
		t.Fatalf("permalink = %q", posts[0].Permalink)
	}
	if posts[0].Flair != "Tickets" || posts[0].Fullname() != "t3_abc123" {
		t.Fatalf("unexpected post: %+v", posts[0])
	}
	if !posts[0].Created.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("created = %s", posts[0].Created)
	}
}

func TestListingAuthenticatedUsesToken(t *testing.T) {
	var tokenCalls int
	var gotAuth string

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls++
		if user, pass, ok := r.BasicAuth(); !ok || user != "id" || pass != "secret" {
			t.Errorf("basic auth = %q/%q ok=%v", user, pass, ok)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q, want client_credentials", got)
		}
		fmt.Fprint(w, `{"access_token":"tok","expires_in":3600}`)
	}))
	defer tokenSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/r/fontainesdc/new" {
			t.Errorf("path = %q, want the OAuth form without .json", r.URL.Path)
		}
		fmt.Fprint(w, sampleListing)
	}))
	defer apiSrv.Close()

	c := New(Options{ClientID: "id", ClientSecret: "secret", UserAgent: "test"})
	c.oauthBase, c.tokenURL = apiSrv.URL, tokenSrv.URL

	for i := 0; i < 2; i++ {
		if _, err := c.Listing(context.Background(), "fontainesdc", "new", 10); err != nil {
			t.Fatalf("Listing: %v", err)
		}
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if tokenCalls != 1 {
		t.Fatalf("token fetched %d times, want it cached after the first", tokenCalls)
	}
}

func TestPasswordGrant(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if got := r.PostForm.Get("grant_type"); got != "password" {
			t.Errorf("grant_type = %q, want password", got)
		}
		if got := r.PostForm.Get("username"); got != "user" {
			t.Errorf("username = %q", got)
		}
		fmt.Fprint(w, `{"access_token":"tok","expires_in":3600}`)
	}))
	defer tokenSrv.Close()
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, sampleListing)
	}))
	defer apiSrv.Close()

	c := New(Options{ClientID: "id", ClientSecret: "secret", Username: "user", Password: "pw"})
	c.oauthBase, c.tokenURL = apiSrv.URL, tokenSrv.URL
	if _, err := c.Listing(context.Background(), "sub", "new", 10); err != nil {
		t.Fatalf("Listing: %v", err)
	}
}

func TestRateLimitIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New(Options{})
	c.publicBase = srv.URL

	_, err := c.Listing(context.Background(), "sub", "new", 10)
	var re *RetryableError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want a RetryableError", err)
	}
	if re.RetryAfter != 7*time.Second {
		t.Fatalf("RetryAfter = %s, want 7s", re.RetryAfter)
	}
}

func TestForbiddenIsNotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(Options{})
	c.publicBase = srv.URL

	_, err := c.Listing(context.Background(), "sub", "new", 10)
	var re *RetryableError
	if err == nil || errors.As(err, &re) {
		t.Fatalf("err = %v, want a permanent error", err)
	}
	if !strings.Contains(err.Error(), "REDDIT_CLIENT_ID") {
		t.Fatalf("a 403 without credentials should hint at auth, got %v", err)
	}
}
