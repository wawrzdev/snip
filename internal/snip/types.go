package snip

import "context"

type Source struct {
	Provider string
	Host     string
	Account  string
}

func (s Source) Key() string { return s.Provider + "/" + s.Account }

type Item struct {
	Source
	ID          string
	Title       string
	Description string
	Files       []string
	URL         string
	CloneURL    string
	LocalPath   string
}

func (i Item) Name() string {
	if i.Title != "" {
		return i.Title
	}
	if i.Description != "" {
		return i.Description
	}
	if len(i.Files) > 0 {
		return i.Files[0]
	}
	return "snippet-" + i.ID
}

type CreateRequest struct {
	Filename    string
	Description string
	Content     []byte
	Public      bool
}

type Provider interface {
	Source(context.Context, providerConfig) (Source, error)
	List(context.Context, Source) ([]Item, error)
	Get(context.Context, Source, string) (Item, error)
	Create(context.Context, Source, CreateRequest) (Item, error)
}
