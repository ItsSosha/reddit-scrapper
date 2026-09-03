package match

import (
	"testing"

	"github.com/ItsSosha/reddit-scrapper/internal/config"
	"github.com/ItsSosha/reddit-scrapper/internal/reddit"
)

func TestMatch(t *testing.T) {
	cfg := config.Match{
		AllOf:     []string{"berlin"},
		AnyOf:     []string{"ticket", "spare"},
		NoneOf:    []string{"looking for"},
		Fields:    []string{"title", "selftext", "flair"},
		WholeWord: true,
	}
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		name string
		post reddit.Post
		want bool
	}{
		{
			name: "title hit",
			post: reddit.Post{Title: "Spare ticket for Berlin show"},
			want: true,
		},
		{
			name: "case insensitive across fields",
			post: reddit.Post{Title: "BERLIN", SelfText: "I have a spare"},
			want: true,
		},
		{
			name: "missing all_of term",
			post: reddit.Post{Title: "Spare ticket for Hamburg"},
			want: false,
		},
		{
			name: "missing any_of term",
			post: reddit.Post{Title: "Berlin setlist discussion"},
			want: false,
		},
		{
			name: "excluded by none_of",
			post: reddit.Post{Title: "Looking for a Berlin ticket"},
			want: false,
		},
		{
			name: "whole word does not match substring",
			post: reddit.Post{Title: "Berlin ticketing website is down", SelfText: ""},
			want: false,
		},
		{
			name: "flair is searched",
			post: reddit.Post{Title: "Berlin", Flair: "Ticket sale"},
			want: true,
		},
		{
			name: "terms must not span field boundaries",
			post: reddit.Post{Title: "Berlin", SelfText: "sale"},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.Match(tc.post).Matched; got != tc.want {
				t.Fatalf("Match(%q/%q) = %v, want %v", tc.post.Title, tc.post.SelfText, got, tc.want)
			}
		})
	}
}

func TestMatchReportsTerms(t *testing.T) {
	m, err := New(config.Match{AllOf: []string{"berlin"}, AnyOf: []string{"ticket", "spare"}, Fields: []string{"title"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res := m.Match(reddit.Post{Title: "spare berlin ticket"})
	if !res.Matched {
		t.Fatal("expected a match")
	}
	if len(res.Terms) != 3 {
		t.Fatalf("Terms = %v, want all three terms reported", res.Terms)
	}
}

func TestAnyOfOnly(t *testing.T) {
	m, err := New(config.Match{AnyOf: []string{"berlin"}, Fields: []string{"title"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !m.Match(reddit.Post{Title: "berlin"}).Matched {
		t.Fatal("any_of alone should match")
	}
	if m.Match(reddit.Post{Title: "dublin"}).Matched {
		t.Fatal("any_of alone should not match unrelated text")
	}
}

func TestPhraseWithPunctuation(t *testing.T) {
	m, err := New(config.Match{AnyOf: []string{"£50", "sold out!"}, Fields: []string{"title"}, WholeWord: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !m.Match(reddit.Post{Title: "selling for £50 face value"}).Matched {
		t.Fatal("term starting with punctuation should still match")
	}
	if !m.Match(reddit.Post{Title: "it is sold out! sadly"}).Matched {
		t.Fatal("term ending with punctuation should still match")
	}
}
