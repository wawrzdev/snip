package snip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	mu       sync.Mutex
	calls    []call
	outputFn func(string, []string) ([]byte, error)
	runFn    func(io.Reader, io.Writer, io.Writer, string, []string) error
}

func (f *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{name, append([]string(nil), args...)})
	f.mu.Unlock()
	if f.outputFn == nil {
		return nil, nil
	}
	return f.outputFn(name, args)
}

func (f *fakeRunner) Run(_ context.Context, in io.Reader, out, errOut io.Writer, name string, args ...string) error {
	f.mu.Lock()
	f.calls = append(f.calls, call{name, append([]string(nil), args...)})
	f.mu.Unlock()
	if f.runFn == nil {
		return nil
	}
	return f.runFn(in, out, errOut, name, args)
}

func (f *fakeRunner) count(name, contains string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.name == name && strings.Contains(strings.Join(c.args, " "), contains) {
			n++
		}
	}
	return n
}

func newTestApp(t *testing.T, runner *fakeRunner) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	app := NewApp(strings.NewReader(""), out, errOut)
	app.Runner, app.IsTTY = runner, func() bool { return false }
	app.LoadConfig = func() (Config, error) { return defaultConfig(), nil }
	return app, out, errOut
}

func githubFixture(name string, args []string) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case name == "git" && strings.Contains(joined, "rev-parse --is-inside-work-tree"):
		return []byte("true\n"), nil
	case name == "git" && strings.Contains(joined, "remote get-url origin"):
		if strings.Contains(joined, "cafebabe1234") {
			return []byte("https://gist.github.com/cafebabe1234.git\n"), nil
		}
		return []byte("https://gist.github.com/abcdef123456.git\n"), nil
	case name == "gh" && strings.HasPrefix(joined, "auth status"):
		return []byte("ok"), nil
	case name == "gh" && joined == "api --hostname github.com user":
		return []byte(`{"login":"alice"}`), nil
	case name == "gh" && strings.Contains(joined, "gists?per_page=100"):
		return []byte(`[{"id":"abcdef123456","description":"Useful JSON","html_url":"https://gist.github.com/alice/abcdef123456","git_pull_url":"https://gist.github.com/abcdef123456.git","files":{"data.json":{}}}]`), nil
	default:
		return nil, fmt.Errorf("unexpected command: %s %s", name, joined)
	}
}

func makeLocal(t *testing.T, item Item, name string) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, "snip", safeComponent(item.Host), safeComponent(item.Account), name)
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	item.LocalPath = path
	if err := writeMetadata(path, item); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHelpLocksApprovedSurface(t *testing.T) {
	app, out, _ := newTestApp(t, &fakeRunner{})
	if err := app.Run(context.Background(), []string{"help"}); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, command := range []string{"list", "get", "new", "paste", "file", "web"} {
		if !strings.Contains(help, "snip "+command) {
			t.Errorf("help omits %s", command)
		}
	}
	for _, command := range []string{"status", "sync", "push", "visibility"} {
		if strings.Contains(help, "snip "+command) {
			t.Errorf("help advertises forbidden %s", command)
		}
		if err := app.Run(context.Background(), []string{command}); err == nil || !strings.Contains(err.Error(), "ordinary Git") {
			t.Errorf("%s should be rejected, got %v", command, err)
		}
	}
}

func TestListIsStableAndNeverInvokesFzf(t *testing.T) {
	runner := &fakeRunner{outputFn: githubFixture}
	app, out, _ := newTestApp(t, runner)
	if err := app.Run(context.Background(), []string{"list"}); err != nil {
		t.Fatal(err)
	}
	want := "github\tgithub.com\talice\tabcdef123456\tno\tUseful JSON\tdata.json\thttps://gist.github.com/alice/abcdef123456\n"
	if out.String() != want {
		t.Fatalf("list output:\n%s\nwant:\n%s", out.String(), want)
	}
	if runner.count("fzf", "") != 0 {
		t.Fatal("list invoked fzf")
	}
}

func TestBrowseUniquePartialClonesAndPrintsVerifiedPath(t *testing.T) {
	runner := &fakeRunner{outputFn: githubFixture}
	runner.runFn = func(_ io.Reader, _, _ io.Writer, name string, args []string) error {
		if name != "git" || len(args) < 2 || args[0] != "clone" {
			return fmt.Errorf("unexpected run: %s %v", name, args)
		}
		return os.MkdirAll(filepath.Join(args[len(args)-1], ".git"), 0o700)
	}
	app, out, _ := newTestApp(t, runner)
	if err := app.Run(context.Background(), []string{"json"}); err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(out.String())
	if !strings.HasSuffix(path, filepath.Join("snip", "github.com", "alice", "useful-json")) {
		t.Fatalf("unexpected path %s", path)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Fatal(err)
	}
}

func TestExistingClonePathStaysStableWhenDescriptionChanges(t *testing.T) {
	runner := &fakeRunner{outputFn: githubFixture}
	app, out, _ := newTestApp(t, runner)
	existing := makeLocal(t, Item{Source: Source{Provider: "github", Host: "github.com", Account: "alice"}, ID: "abcdef123456", Description: "Old name", CloneURL: "https://gist.github.com/abcdef123456.git"}, "old-name")
	if err := app.Run(context.Background(), []string{"get", "Useful JSON"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != existing {
		t.Fatalf("got %q want %q", strings.TrimSpace(out.String()), existing)
	}
	if runner.count("git", "clone") != 0 {
		t.Fatal("existing clone was cloned again")
	}
}

func TestOfflineShowsOnlyLocalClones(t *testing.T) {
	runner := &fakeRunner{outputFn: func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "git" && strings.Contains(joined, "rev-parse") {
			return []byte("true\n"), nil
		}
		if name == "git" && strings.Contains(joined, "remote get-url") {
			return []byte("https://gist.github.com/deadbeef.git\n"), nil
		}
		if strings.HasPrefix(joined, "auth status") {
			return []byte("ok"), nil
		}
		if joined == "api --hostname github.com user" {
			return []byte(`{"login":"alice"}`), nil
		}
		return nil, errors.New("offline")
	}}
	app, out, errOut := newTestApp(t, runner)
	makeLocal(t, Item{Source: Source{Provider: "github", Host: "github.com", Account: "alice"}, ID: "deadbeef", Description: "local tool", CloneURL: "https://gist.github.com/deadbeef.git"}, "local-tool")
	if err := app.Run(context.Background(), []string{"list"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "deadbeef\tyes\tlocal tool") {
		t.Fatalf("missing local item: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "showing local clones only") {
		t.Fatalf("missing offline notice: %s", errOut.String())
	}
}

func TestAuthenticationFailureDoesNotFallBack(t *testing.T) {
	runner := &fakeRunner{outputFn: func(string, []string) ([]byte, error) { return nil, errors.New("not logged in") }}
	app, _, _ := newTestApp(t, runner)
	err := app.Run(context.Background(), []string{"list"})
	if err == nil || !strings.Contains(err.Error(), "gh auth login --hostname github.com") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetPartialNeedsInteractivePicker(t *testing.T) {
	app, _, _ := newTestApp(t, &fakeRunner{outputFn: githubFixture})
	err := app.Run(context.Background(), []string{"get", "Useful"})
	if err == nil || !strings.Contains(err.Error(), "interactive picker") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFzfCancellationDoesNotClone(t *testing.T) {
	runner := &fakeRunner{outputFn: githubFixture, runFn: func(_ io.Reader, _, _ io.Writer, name string, _ []string) error {
		if name == "fzf" {
			return errors.New("exit 130")
		}
		return nil
	}}
	app, _, _ := newTestApp(t, runner)
	app.IsTTY = func() bool { return true }
	err := app.Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.count("git", "clone") != 0 {
		t.Fatal("cancelled picker mutated state")
	}
}

func TestFileCreationPersistsIdentityBeforeCloneAndRetriesWithoutDuplicate(t *testing.T) {
	createCalls, cloneCalls := 0, 0
	runner := &fakeRunner{}
	runner.outputFn = func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "git" && strings.Contains(joined, "rev-parse --is-inside-work-tree"):
			return []byte("true\n"), nil
		case name == "git" && strings.Contains(joined, "remote get-url origin"):
			return []byte("https://gist.github.com/cafebabe1234.git\n"), nil
		case name == "gh" && strings.HasPrefix(joined, "auth status"):
			return []byte("ok"), nil
		case name == "gh" && joined == "api --hostname github.com user":
			return []byte(`{"login":"alice"}`), nil
		case name == "gh" && strings.HasPrefix(joined, "gist create"):
			createCalls++
			return []byte("https://gist.github.com/alice/cafebabe1234\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, joined)
		}
	}
	runner.runFn = func(_ io.Reader, _, _ io.Writer, name string, args []string) error {
		if name != "git" {
			return fmt.Errorf("unexpected %s", name)
		}
		cloneCalls++
		if cloneCalls == 1 {
			return errors.New("network lost")
		}
		return os.MkdirAll(filepath.Join(args[len(args)-1], ".git"), 0o700)
	}
	app, out, _ := newTestApp(t, runner)
	source := filepath.Join(t.TempDir(), "demo.go")
	if err := os.WriteFile(source, []byte("package demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"file", source, "--description", "Demo"}
	if err := app.Run(context.Background(), args); err == nil || !strings.Contains(err.Error(), "remote retained") {
		t.Fatalf("first run: %v", err)
	}
	stateDir := filepath.Join(os.Getenv("XDG_STATE_HOME"), "snip", "pending")
	entries, err := os.ReadDir(stateDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("pending intent missing: %v, %v", entries, err)
	}
	info, err := entries[0].Info()
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("pending intent mode: %v, %v", info, err)
	}
	intentData, err := os.ReadFile(filepath.Join(stateDir, entries[0].Name()))
	var saved creationIntent
	if err != nil || json.Unmarshal(intentData, &saved) != nil || string(saved.Request.Content) != "package demo\n" || saved.Status != "created" {
		t.Fatalf("pending intent incomplete: %s (%v)", intentData, err)
	}
	if err := app.Run(context.Background(), args); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if createCalls != 1 {
		t.Fatalf("created %d remotes, want 1", createCalls)
	}
	if got, _ := os.ReadFile(source); string(got) != "package demo\n" {
		t.Fatalf("source changed: %q", got)
	}
	if !strings.Contains(out.String(), filepath.Join("github.com", "alice", "demo")) {
		t.Fatalf("missing canonical path: %s", out.String())
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	foundCloneURL := false
	for _, command := range runner.calls {
		if command.name == "git" && strings.Contains(strings.Join(command.args, " "), "https://gist.github.com/cafebabe1234.git") {
			foundCloneURL = true
		}
	}
	if !foundCloneURL {
		t.Fatal("creation did not use the canonical gist clone URL")
	}
}

func TestUnsafePasteFilenameFailsBeforeClipboard(t *testing.T) {
	runner := &fakeRunner{outputFn: githubFixture}
	app, _, _ := newTestApp(t, runner)
	app.LookPath = func(string) (string, error) { return "/fake", nil }
	err := app.Run(context.Background(), []string{"paste", "../secret"})
	if err == nil || !strings.Contains(err.Error(), "unsafe filename") {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.count("pbpaste", "") != 0 {
		t.Fatal("clipboard read for unsafe filename")
	}
}

func TestUpdateFailureReportsPreservedChanges(t *testing.T) {
	runner := &fakeRunner{outputFn: githubFixture, runFn: func(_ io.Reader, _, _ io.Writer, name string, args []string) error {
		if name == "git" && len(args) > 0 && args[0] == "-C" {
			return errors.New("dirty worktree")
		}
		return nil
	}}
	app, _, _ := newTestApp(t, runner)
	makeLocal(t, Item{Source: Source{Provider: "github", Host: "github.com", Account: "alice"}, ID: "abcdef123456", Description: "Useful JSON", CloneURL: "https://gist.github.com/abcdef123456.git"}, "useful-json")
	err := app.Run(context.Background(), []string{"get", "--update", "Useful JSON"})
	if err == nil || !strings.Contains(err.Error(), "local changes were preserved") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigParsingAndXDGPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	xdg := filepath.Join(home, "cfg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	path := filepath.Join(xdg, "snip", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "default_provider = \"gitlab\"\n[github]\nenabled = false\nhost = \"github.com\"\n[gitlab]\nhost = \"git.corp.example\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProvider != "gitlab" || !cfg.GitLab.Enabled || cfg.GitLab.Host != "git.corp.example" || cfg.GitHub.Enabled {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
