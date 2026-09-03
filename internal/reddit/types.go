// Package reddit is a small read-only client for subreddit listings.
package reddit

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// Post is the subset of a Reddit link we care about.
type Post struct {
	ID        string
	Subreddit string
	Title     string
	SelfText  string
	Flair     string
	Author    string
	URL       string
	Permalink string
	Created   time.Time
	NSFW      bool
}

// listing mirrors the JSON envelope Reddit wraps every listing in.
type listing struct {
	Data struct {
		After    string `json:"after"`
		Children []struct {
			Data struct {
				ID         string  `json:"id"`
				Subreddit  string  `json:"subreddit"`
				Title      string  `json:"title"`
				SelfText   string  `json:"selftext"`
				LinkFlair  string  `json:"link_flair_text"`
				Author     string  `json:"author"`
				URL        string  `json:"url"`
				Permalink  string  `json:"permalink"`
				CreatedUTC float64 `json:"created_utc"`
				Over18     bool    `json:"over_18"`
			} `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

func (l listing) posts() []Post {
	out := make([]Post, 0, len(l.Data.Children))
	for _, c := range l.Data.Children {
		d := c.Data
		out = append(out, Post{
			ID:        d.ID,
			Subreddit: d.Subreddit,
			// Reddit HTML-escapes ampersands and quotes in titles and bodies.
			Title:     html.UnescapeString(d.Title),
			SelfText:  html.UnescapeString(d.SelfText),
			Flair:     html.UnescapeString(d.LinkFlair),
			Author:    d.Author,
			URL:       d.URL,
			Permalink: "https://www.reddit.com" + d.Permalink,
			Created:   time.Unix(int64(d.CreatedUTC), 0).UTC(),
			NSFW:      d.Over18,
		})
	}
	return out
}

// Field returns the named searchable field of a post.
func (p Post) Field(name string) string {
	switch strings.ToLower(name) {
	case "title":
		return p.Title
	case "selftext", "body", "text":
		return p.SelfText
	case "flair":
		return p.Flair
	case "author":
		return p.Author
	case "url":
		return p.URL
	default:
		return ""
	}
}

// Fullname is the "t3_xxxxx" form Reddit uses for links.
func (p Post) Fullname() string { return "t3_" + p.ID }

func (p Post) String() string {
	return fmt.Sprintf("r/%s [%s] %s (%s)", p.Subreddit, p.Created.Format(time.RFC3339), p.Title, p.Permalink)
}
