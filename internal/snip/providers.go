package snip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type cliProvider struct {
	kind   string
	runner Runner
}

type authError struct{ message string }

func (e authError) Error() string { return e.message }

type offlineError struct{ err error }

func (e offlineError) Error() string { return e.err.Error() }
func (e offlineError) Unwrap() error { return e.err }

func providerAuthError(provider, host string) error {
	guidance := "gh auth login --hostname " + host
	if provider == "gitlab" {
		guidance = "glab auth login --hostname " + host
	}
	return authError{message: fmt.Sprintf("%s authentication is required; run `%s`", provider, guidance)}
}

type gitLabFile struct {
	Path string `json:"path"`
}

type gitLabRow struct {
	ID            json.Number  `json:"id"`
	Title         string       `json:"title"`
	Description   string       `json:"description"`
	WebURL        string       `json:"web_url"`
	FileName      string       `json:"file_name"`
	Files         []gitLabFile `json:"files"`
	HTTPURLToRepo string       `json:"http_url_to_repo"`
	ProjectID     *json.Number `json:"project_id"`
}

func (p cliProvider) Source(ctx context.Context, cfg providerConfig) (Source, error) {
	var authArgs, userArgs []string
	if p.kind == "github" {
		authArgs = []string{"auth", "status", "--hostname", cfg.Host}
		userArgs = []string{"api", "--hostname", cfg.Host, "user"}
	} else {
		authArgs = []string{"auth", "status", "--hostname", cfg.Host}
		userArgs = []string{"api", "--hostname", cfg.Host, "/user"}
	}
	cli := map[string]string{"github": "gh", "gitlab": "glab"}[p.kind]
	if _, err := p.runner.Output(ctx, cli, authArgs...); err != nil {
		if looksLikeAuthFailure(err) {
			return Source{}, providerAuthError(p.kind, cfg.Host)
		}
		return Source{}, offlineError{err: fmt.Errorf("resolve %s authentication on %s: %w", p.kind, cfg.Host, err)}
	}
	out, err := p.runner.Output(ctx, cli, userArgs...)
	if err != nil {
		if looksLikeAuthFailure(err) {
			return Source{}, providerAuthError(p.kind, cfg.Host)
		}
		return Source{}, offlineError{err: fmt.Errorf("resolve %s account on %s: %w", p.kind, cfg.Host, err)}
	}
	var user struct{ Login, Username string }
	if err := json.Unmarshal(out, &user); err != nil {
		return Source{}, fmt.Errorf("decode %s account: %w", p.kind, err)
	}
	account := user.Login
	if p.kind == "gitlab" {
		account = user.Username
	}
	if account == "" {
		return Source{}, fmt.Errorf("%s returned an empty account name", cli)
	}
	return Source{Provider: p.kind, Host: cfg.Host, Account: account}, nil
}

func looksLikeAuthFailure(err error) bool {
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"not logged", "not authenticated", "authentication", "unauthorized", "invalid token", "is invalid", "no token", "401", "login required"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (p cliProvider) List(ctx context.Context, source Source) ([]Item, error) {
	if p.kind == "github" {
		out, err := p.runner.Output(ctx, "gh", "api", "--hostname", source.Host, "--paginate", "gists?per_page=100")
		if err != nil {
			return nil, err
		}
		var rows []struct {
			ID          string                     `json:"id"`
			Description string                     `json:"description"`
			HTMLURL     string                     `json:"html_url"`
			GitPullURL  string                     `json:"git_pull_url"`
			Files       map[string]json.RawMessage `json:"files"`
		}
		if err := decodeArrayPages(out, &rows); err != nil {
			return nil, err
		}
		items := make([]Item, 0, len(rows))
		for _, row := range rows {
			files := make([]string, 0, len(row.Files))
			for name := range row.Files {
				files = append(files, name)
			}
			sort.Strings(files)
			items = append(items, Item{Source: source, ID: row.ID, Description: row.Description, Files: files, URL: row.HTMLURL, CloneURL: row.GitPullURL})
		}
		return items, nil
	}
	out, err := p.runner.Output(ctx, "glab", "api", "--hostname", source.Host, "--paginate", "/snippets?per_page=100")
	if err != nil {
		return nil, err
	}
	var rows []gitLabRow
	if err := decodeArrayPages(out, &rows); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		if row.ProjectID != nil {
			continue
		}
		id := row.ID.String()
		if len(row.Files) == 0 {
			detailOut, err := p.runner.Output(ctx, "glab", "api", "--hostname", source.Host, "/snippets/"+id)
			if err != nil {
				return nil, err
			}
			var detail gitLabRow
			if err := json.Unmarshal(detailOut, &detail); err != nil {
				return nil, err
			}
			if detail.ProjectID != nil {
				continue
			}
			row = detail
		}
		files := make([]string, 0, len(row.Files))
		for _, file := range row.Files {
			if file.Path != "" {
				files = append(files, file.Path)
			}
		}
		if len(files) == 0 && row.FileName != "" {
			files = append(files, row.FileName)
		}
		sort.Strings(files)
		cloneURL := row.HTTPURLToRepo
		if cloneURL == "" {
			cloneURL = "https://" + source.Host + "/-/snippets/" + id + ".git"
		}
		items = append(items, Item{Source: source, ID: id, Title: row.Title, Description: row.Description, Files: files, URL: row.WebURL, CloneURL: cloneURL})
	}
	return items, nil
}

func (p cliProvider) Get(ctx context.Context, source Source, id string) (Item, error) {
	items, err := p.List(ctx, source)
	if err != nil {
		return Item{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Item{}, fmt.Errorf("%s item %q not found", p.kind, id)
}

func (p cliProvider) Create(ctx context.Context, source Source, req CreateRequest) (Item, error) {
	if p.kind == "github" {
		dir, err := tempFileWithName(req.Filename, req.Content)
		if err != nil {
			return Item{}, err
		}
		defer dir.cleanup()
		args := []string{"gist", "create", dir.path, "--desc", req.Description}
		if req.Public {
			args = append(args, "--public")
		}
		out, err := p.runner.Output(ctx, "gh", args...)
		if err != nil {
			return Item{}, err
		}
		u, err := url.Parse(strings.TrimSpace(string(out)))
		if err != nil {
			return Item{}, err
		}
		id := filepath.Base(strings.TrimSuffix(u.Path, "/"))
		if id == "" || id == "." {
			return Item{}, errors.New("gh did not return a gist URL")
		}
		return Item{Source: source, ID: id, Description: req.Description, Files: []string{req.Filename}, URL: strings.TrimSpace(string(out)), CloneURL: u.Scheme + "://" + u.Host + "/" + id + ".git"}, nil
	}
	visibility := "private"
	if req.Public {
		visibility = "public"
	}
	filesJSON, err := json.Marshal([]map[string]string{{"file_path": req.Filename, "content": string(req.Content)}})
	if err != nil {
		return Item{}, err
	}
	args := []string{"api", "--hostname", source.Host, "--method", "POST", "/snippets", "--field", "title=" + firstNonempty(req.Description, strings.TrimSuffix(req.Filename, filepath.Ext(req.Filename))), "--field", "visibility=" + visibility, "--field", "files=" + string(filesJSON)}
	out, err := p.runner.Output(ctx, "glab", args...)
	if err != nil {
		return Item{}, err
	}
	var row gitLabRow
	if err := json.Unmarshal(out, &row); err != nil {
		return Item{}, err
	}
	id := row.ID.String()
	if id == "" {
		return Item{}, errors.New("glab returned an empty snippet ID")
	}
	cloneURL := row.HTTPURLToRepo
	if cloneURL == "" {
		cloneURL = "https://" + source.Host + "/-/snippets/" + id + ".git"
	}
	return Item{Source: source, ID: id, Title: row.Title, Description: row.Description, Files: []string{req.Filename}, URL: row.WebURL, CloneURL: cloneURL}, nil
}

func decodeArrayPages[T any](data []byte, destination *[]T) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var page []T
		if err := decoder.Decode(&page); err != nil {
			return err
		}
		*destination = append(*destination, page...)
	}
	return nil
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "snippet"
}

func parseExplicitID(value string) (provider, host, id string) {
	u, err := url.Parse(value)
	if err == nil && u.Host != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if u.Host == "gist.github.com" && len(parts) > 0 {
			return "github", "github.com", parts[len(parts)-1]
		}
		for i := range parts {
			if parts[i] == "snippets" && i+1 < len(parts) {
				return "gitlab", strings.ToLower(u.Hostname()), strings.TrimSuffix(parts[i+1], ".git")
			}
		}
	}
	if _, err := strconv.ParseUint(value, 10, 64); err == nil {
		return "gitlab", "", value
	}
	if len(value) >= 8 && allHex(value) {
		return "github", "", value
	}
	return "", "", ""
}

func allHex(s string) bool {
	for _, r := range strings.ToLower(s) {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
