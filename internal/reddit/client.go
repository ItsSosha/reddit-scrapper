package reddit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	oauthHost  = "https://oauth.reddit.com"
	publicHost = "https://www.reddit.com"
	tokenURL   = "https://www.reddit.com/api/v1/access_token"
)

// Client fetches subreddit listings. With credentials it uses the OAuth API;
// without them it falls back to the public .json endpoints.
type Client struct {
	httpClient *http.Client
	userAgent  string

	clientID     string
	clientSecret string
	username     string
	password     string

	// Endpoints, overridable in tests.
	oauthBase  string
	publicBase string
	tokenURL   string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// Options configure a Client. Leave the credential fields empty for
// unauthenticated access.
type Options struct {
	ClientID     string
	ClientSecret string
	Username     string
	Password     string
	UserAgent    string
	HTTPClient   *http.Client
	// BaseURL and TokenURL override the Reddit endpoints. They are here for
	// tests and for pointing the client at a proxy; leave them empty to talk
	// to Reddit itself.
	BaseURL  string
	TokenURL string
}

// New returns a Client. UserAgent should be a descriptive string; Reddit
// throttles generic ones hard.
func New(o Options) *Client {
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	ua := o.UserAgent
	if ua == "" {
		ua = "go:reddit-scrapper:0.1.0"
	}
	return &Client{
		httpClient:   hc,
		userAgent:    ua,
		clientID:     o.ClientID,
		clientSecret: o.ClientSecret,
		username:     o.Username,
		password:     o.Password,
		oauthBase:    firstNonEmpty(o.BaseURL, oauthHost),
		publicBase:   firstNonEmpty(o.BaseURL, publicHost),
		tokenURL:     firstNonEmpty(o.TokenURL, tokenURL),
	}
}

// Authenticated reports whether the client has OAuth credentials.
func (c *Client) Authenticated() bool {
	return c.clientID != "" && c.clientSecret != ""
}

// RetryableError marks a failure worth retrying on the next poll (rate limits,
// transient 5xx). The watcher logs these instead of exiting.
type RetryableError struct {
	StatusCode int
	RetryAfter time.Duration
	Err        error
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// Listing fetches up to limit posts from r/<subreddit>, sorted by sort
// ("new", "hot" or "rising"). Posts come back newest first for "new".
func (c *Client) Listing(ctx context.Context, subreddit, sort string, limit int) ([]Post, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("raw_json", "1")

	host := c.publicBase
	path := fmt.Sprintf("/r/%s/%s.json", url.PathEscape(subreddit), url.PathEscape(sort))
	if c.Authenticated() {
		host = c.oauthBase
		path = fmt.Sprintf("/r/%s/%s", url.PathEscape(subreddit), url.PathEscape(sort))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if c.Authenticated() {
		token, err := c.accessToken(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", c.userAgent)

	body, err := c.do(req)
	if err != nil {
		return nil, err
	}

	var l listing
	if err := json.Unmarshal(body, &l); err != nil {
		return nil, fmt.Errorf("decode r/%s listing: %w", subreddit, err)
	}
	return l.posts(), nil
}

// do executes a request and returns the body, classifying failures that are
// worth retrying.
func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Network errors are transient often enough to be worth another poll.
		return nil, &RetryableError{Err: err}
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("%s %s: %s: %s", req.Method, req.URL.Path, resp.Status, snippet(body))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, &RetryableError{
				StatusCode: resp.StatusCode,
				RetryAfter: retryAfter(resp.Header.Get("Retry-After")),
				Err:        err,
			}
		}
		if resp.StatusCode == http.StatusForbidden && !c.Authenticated() {
			return nil, fmt.Errorf("%w (unauthenticated requests are often blocked; set REDDIT_CLIENT_ID/REDDIT_CLIENT_SECRET)", err)
		}
		return nil, err
	}
	if readErr != nil {
		return nil, &RetryableError{Err: fmt.Errorf("read body: %w", readErr)}
	}
	return body, nil
}

// accessToken returns a cached OAuth token, refreshing it when it is close to
// expiry. A username/password pair selects the "script" password grant;
// otherwise the app-only client_credentials grant is used.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}

	form := url.Values{}
	if c.username != "" && c.password != "" {
		form.Set("grant_type", "password")
		form.Set("username", c.username)
		form.Set("password", c.password)
	} else {
		form.Set("grant_type", "client_credentials")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.clientID, c.clientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.userAgent)

	body, err := c.do(req)
	if err != nil {
		return "", fmt.Errorf("reddit token request: %w", err)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tok.Error != "" {
		return "", fmt.Errorf("reddit token request rejected: %s", tok.Error)
	}
	if tok.AccessToken == "" {
		return "", errors.New("reddit token response had no access_token")
	}

	c.token = tok.AccessToken
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	// Refresh a minute early so a token never expires mid-poll.
	c.expires = time.Now().Add(ttl - time.Minute)
	return c.token, nil
}

func retryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return strings.ReplaceAll(s, "\n", " ")
}
