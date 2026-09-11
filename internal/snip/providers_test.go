package snip

import (
	"context"
	"strings"
	"testing"
)

func TestGitLabListUsesAccountLevelEndpoint(t *testing.T) {
	runner := &fakeRunner{outputFn: func(name string, args []string) ([]byte, error) {
		if name != "glab" {
			t.Fatalf("unexpected executable %s", name)
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "/snippets/42") {
			return []byte(`{"id":42,"title":"Deploy","description":"ops","web_url":"https://git.example/-/snippets/42","http_url_to_repo":"https://git.example/snippets/42.git","files":[{"path":"deploy.sh"},{"path":"values.yaml"}],"project_id":null}`), nil
		}
		if !strings.Contains(joined, "/snippets?per_page=100") {
			t.Fatalf("project endpoint used: %v", args)
		}
		return []byte(`[{"id":42,"title":"Deploy","description":"ops","web_url":"https://git.example/-/snippets/42","file_name":"deploy.sh","project_id":null}]`), nil
	}}
	p := cliProvider{kind: "gitlab", runner: runner}
	items, err := p.List(context.Background(), Source{Provider: "gitlab", Host: "git.example", Account: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "42" || items[0].CloneURL != "https://git.example/snippets/42.git" || strings.Join(items[0].Files, ",") != "deploy.sh,values.yaml" {
		t.Fatalf("unexpected item: %+v", items)
	}
}

func TestGitLabListRejectsProjectSnippets(t *testing.T) {
	runner := &fakeRunner{outputFn: func(_ string, _ []string) ([]byte, error) {
		return []byte(`[{"id":7,"title":"project","project_id":99,"files":[{"path":"bad.txt"}]}]`), nil
	}}
	p := cliProvider{kind: "gitlab", runner: runner}
	items, err := p.List(context.Background(), Source{Provider: "gitlab", Host: "git.example", Account: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("project snippet leaked into inventory: %+v", items)
	}
}

func TestExplicitIdentifiers(t *testing.T) {
	tests := map[string][3]string{
		"https://gist.github.com/alice/abcdef123456": {"github", "github.com", "abcdef123456"},
		"https://git.example/-/snippets/42":          {"gitlab", "git.example", "42"},
		"42":                                         {"gitlab", "", "42"},
		"abcdef123456":                               {"github", "", "abcdef123456"},
	}
	for input, want := range tests {
		provider, host, id := parseExplicitID(input)
		if provider != want[0] || host != want[1] || id != want[2] {
			t.Errorf("%q: got %q/%q/%q want %q/%q/%q", input, provider, host, id, want[0], want[1], want[2])
		}
	}
}

func TestGitHubPaginationCombinesSequentialArrays(t *testing.T) {
	runner := &fakeRunner{outputFn: func(_ string, _ []string) ([]byte, error) {
		return []byte(`[{"id":"a","files":{"a.txt":{}}}]
[{"id":"b","files":{"b.txt":{}}}]`), nil
	}}
	p := cliProvider{kind: "github", runner: runner}
	items, err := p.List(context.Background(), Source{Provider: "github", Host: "github.com", Account: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "a" || items[1].ID != "b" {
		t.Fatalf("unexpected pages: %+v", items)
	}
}
