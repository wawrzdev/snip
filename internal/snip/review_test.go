package snip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/creack/pty"
)

func TestWrapperCapturedStdoutStillUsesFzf(t *testing.T) {
	runner := &fakeRunner{outputFn: githubFixture}
	runner.runFn = func(in io.Reader, out, _ io.Writer, name string, args []string) error {
		switch name {
		case "fzf":
			data, _ := io.ReadAll(in)
			if !strings.Contains(string(data), "Useful JSON") {
				return errors.New("missing candidate")
			}
			_, _ = io.WriteString(out, "0\tselected\n")
			return nil
		case "git":
			return os.MkdirAll(filepath.Join(args[len(args)-1], ".git"), 0o700)
		default:
			return fmt.Errorf("unexpected command %s", name)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	master, input, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer input.Close()
	captured, errOut := new(bytes.Buffer), new(bytes.Buffer)
	app := NewApp(input, captured, errOut)
	app.Runner = runner
	app.LoadConfig = func() (Config, error) { return defaultConfig(), nil }
	if !app.IsTTY() {
		t.Fatal("captured stdout incorrectly disabled input interactivity")
	}
	if err := app.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if runner.count("fzf", "") != 1 {
		t.Fatal("captured stdout disabled fzf")
	}
	if !strings.HasSuffix(strings.TrimSpace(captured.String()), filepath.Join("github.com", "alice", "useful-json")) {
		t.Fatalf("unexpected wrapper output %q", captured.String())
	}
}

func TestDevNullIsNotInteractive(t *testing.T) {
	input, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	app := NewApp(input, io.Discard, io.Discard)
	if app.IsTTY() {
		t.Fatal("/dev/null was incorrectly treated as a terminal")
	}
}

func TestReadablePathAndIDSuffixOnlyOnCollision(t *testing.T) {
	runner := &fakeRunner{}
	runner.outputFn = func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "git" && strings.Contains(joined, "rev-parse") {
			return []byte("true\n"), nil
		}
		if name == "git" && strings.Contains(joined, "remote get-url") {
			if strings.Contains(joined, "useful-json-abcdef123456") {
				return []byte("https://gist.github.com/abcdef123456.git\n"), nil
			}
			return []byte("https://gist.github.com/other1234.git\n"), nil
		}
		return nil, fmt.Errorf("unexpected output %s %v", name, args)
	}
	runner.runFn = func(_ io.Reader, _, _ io.Writer, name string, args []string) error {
		if name != "git" || args[0] != "clone" {
			return fmt.Errorf("unexpected run %s %v", name, args)
		}
		return os.MkdirAll(filepath.Join(args[len(args)-1], ".git"), 0o700)
	}
	app, _, _ := newTestApp(t, runner)
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, "snip", "github.com", "alice", "useful-json")
	if err := os.MkdirAll(filepath.Join(base, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	other := Item{Source: Source{Provider: "github", Host: "github.com", Account: "alice"}, ID: "other1234", Description: "Useful JSON", CloneURL: "https://gist.github.com/other1234.git"}
	if err := writeMetadata(base, other); err != nil {
		t.Fatal(err)
	}
	item := Item{Source: Source{Provider: "github", Host: "github.com", Account: "alice"}, ID: "abcdef123456", Description: "Useful JSON", CloneURL: "https://gist.github.com/abcdef123456.git"}
	path, err := app.clone(context.Background(), item, false)
	if err != nil {
		t.Fatal(err)
	}
	want := base + "-abcdef123456"
	if path != want {
		t.Fatalf("got %q want %q", path, want)
	}
}

func TestPathFallbacksAndComponentSanitization(t *testing.T) {
	root := "/tmp/root"
	withFile := Item{Source: Source{Host: "Git.Example.COM", Account: "Alice/Dev"}, ID: "42", Files: []string{"dir/example.go"}}
	if got := filepath.Join(root, safeComponent(withFile.Host), safeComponent(withFile.Account), slug(withFile.Name())); got != "/tmp/root/git.example.com/alice-dev/example" {
		t.Fatalf("filename path: %s", got)
	}
	withoutName := Item{Source: withFile.Source, ID: "42"}
	if slug(withoutName.Name()) != "snippet-42" {
		t.Fatalf("fallback name %q", withoutName.Name())
	}
}

func TestRootUsesConfiguredHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	root, err := app.root()
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(home, "snip") {
		t.Fatalf("got %q want home-relative snip root", root)
	}
}

func TestFullyOfflineStartupAndExplicitLocalID(t *testing.T) {
	runner := &fakeRunner{outputFn: func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "git" && strings.Contains(joined, "rev-parse") {
			return []byte("true\n"), nil
		}
		if name == "git" && strings.Contains(joined, "remote get-url") {
			return []byte("https://gist.github.com/deadbeef.git\n"), nil
		}
		return nil, errors.New("dial tcp: network unreachable")
	}}
	app, out, errOut := newTestApp(t, runner)
	path := makeLocal(t, Item{Source: Source{Provider: "github", Host: "github.com", Account: "alice"}, ID: "deadbeef", Description: "offline item", CloneURL: "https://gist.github.com/deadbeef.git"}, "offline-item")
	if err := app.Run(context.Background(), []string{"get", "deadbeef"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != path {
		t.Fatalf("got %q want %q", out.String(), path)
	}
	if !strings.Contains(errOut.String(), "showing local clones only") {
		t.Fatalf("missing notice: %s", errOut.String())
	}
}

func TestExpiredAuthDuringListingRemainsHardError(t *testing.T) {
	runner := &fakeRunner{outputFn: func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "gh" && strings.HasPrefix(joined, "auth status") {
			return []byte("ok"), nil
		}
		if name == "gh" && joined == "api --hostname github.com user" {
			return []byte(`{"login":"alice"}`), nil
		}
		return nil, errors.New("HTTP 401 Unauthorized")
	}}
	app, _, _ := newTestApp(t, runner)
	err := app.Run(context.Background(), []string{"list"})
	if err == nil || !strings.Contains(err.Error(), "gh auth login --hostname github.com") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExplicitGitLabURLRejectsConfiguredHostMismatch(t *testing.T) {
	runner := &fakeRunner{}
	app, _, _ := newTestApp(t, runner)
	app.LoadConfig = func() (Config, error) {
		return Config{DefaultProvider: "gitlab", GitLab: providerConfig{Enabled: true, Host: "git.corp.example"}}, nil
	}
	err := app.Run(context.Background(), []string{"get", "https://other.example/-/snippets/42"})
	if err == nil || !strings.Contains(err.Error(), "does not match configured host") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("mismatched URL queried a provider")
	}
}

func TestUnrelatedGitOriginIsNeverUpdatedOrReturned(t *testing.T) {
	runner := &fakeRunner{outputFn: func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "git" && strings.Contains(joined, "rev-parse") {
			return []byte("true\n"), nil
		}
		if name == "git" && strings.Contains(joined, "remote get-url") {
			return []byte("https://gist.github.com/not-the-item.git\n"), nil
		}
		return githubFixture(name, args)
	}}
	app, _, _ := newTestApp(t, runner)
	makeLocal(t, Item{Source: Source{Provider: "github", Host: "github.com", Account: "alice"}, ID: "abcdef123456", Description: "Useful JSON", CloneURL: "https://gist.github.com/abcdef123456.git"}, "useful-json")
	err := app.Run(context.Background(), []string{"get", "--update", "Useful JSON"})
	if err == nil || !strings.Contains(err.Error(), "origin does not match") {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.count("git", "pull --ff-only") != 0 {
		t.Fatal("unrelated repository was mutated")
	}
}

func TestInventoryRejectsCopiedMetadataWithWrongOrigin(t *testing.T) {
	runner := &fakeRunner{outputFn: func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "git" && strings.Contains(joined, "rev-parse") {
			return []byte("true\n"), nil
		}
		if name == "git" && strings.Contains(joined, "remote get-url") {
			return []byte("https://gist.github.com/a-different-id.git\n"), nil
		}
		return githubFixture(name, args)
	}}
	app, out, _ := newTestApp(t, runner)
	makeLocal(t, Item{Source: Source{Provider: "github", Host: "github.com", Account: "alice"}, ID: "abcdef123456", Description: "Useful JSON", CloneURL: "https://gist.github.com/abcdef123456.git"}, "copied")
	if err := app.Run(context.Background(), []string{"list"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\tno\t") || strings.Contains(out.String(), "\tyes\t") {
		t.Fatalf("invalid clone was claimed as cloned: %q", out.String())
	}
}

func TestInventoryRejectsMetadataInNonRepositoryDirectory(t *testing.T) {
	runner := &fakeRunner{outputFn: func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "git" && strings.Contains(joined, "rev-parse") {
			return []byte("false\n"), nil
		}
		return githubFixture(name, args)
	}}
	app, out, _ := newTestApp(t, runner)
	makeLocal(t, Item{Source: Source{Provider: "github", Host: "github.com", Account: "alice"}, ID: "abcdef123456", Description: "Useful JSON", CloneURL: "https://gist.github.com/abcdef123456.git"}, "not-a-repository")
	if err := app.Run(context.Background(), []string{"list"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\tno\t") || strings.Contains(out.String(), "\tyes\t") {
		t.Fatalf("non-repository metadata was claimed as cloned: %q", out.String())
	}
}

func TestOfflinePickerDoesNotOfferInvalidLocalMetadata(t *testing.T) {
	runner := &fakeRunner{outputFn: func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "git" && strings.Contains(joined, "rev-parse"):
			return []byte("true\n"), nil
		case name == "git" && strings.Contains(joined, "remote get-url"):
			return []byte("https://gist.github.com/wrong.git\n"), nil
		default:
			return nil, errors.New("network unavailable")
		}
	}}
	app, _, _ := newTestApp(t, runner)
	app.IsTTY = func() bool { return true }
	makeLocal(t, Item{Source: Source{Provider: "github", Host: "github.com", Account: "alice"}, ID: "deadbeef", Description: "copied", CloneURL: "https://gist.github.com/deadbeef.git"}, "copied")
	err := app.Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "no snippets found") {
		t.Fatalf("unexpected result: %v", err)
	}
	if runner.count("fzf", "") != 0 {
		t.Fatal("picker received an invalid local clone")
	}
}

func TestFzfCandidateFieldsCannotForgeRecords(t *testing.T) {
	var feed string
	runner := &fakeRunner{runFn: func(in io.Reader, out, _ io.Writer, name string, _ []string) error {
		if name != "fzf" {
			return fmt.Errorf("unexpected %s", name)
		}
		data, _ := io.ReadAll(in)
		feed = string(data)
		_, _ = io.WriteString(out, "1\tselected\n")
		return nil
	}}
	app, _, _ := newTestApp(t, runner)
	app.IsTTY = func() bool { return true }
	items := []Item{{ID: "safe", Description: "safe"}, {ID: "evil", Description: "bad\n0\tforged", Files: []string{"x\ty"}}}
	chosen, err := app.fzf(context.Background(), items, "")
	if err != nil {
		t.Fatal(err)
	}
	if chosen.ID != "evil" {
		t.Fatalf("wrong selection %+v", chosen)
	}
	lines := strings.Split(strings.TrimSuffix(feed, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("forged line in feed %q", feed)
	}
	for _, line := range lines {
		if strings.Count(line, "\t") != 1 {
			t.Fatalf("forged field in %q", line)
		}
	}
}

func TestContextualCompletions(t *testing.T) {
	completionFiles := map[string]string{"bash": "snip.bash", "zsh": "_snip", "fish": "snip.fish"}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		app, out, _ := newTestApp(t, &fakeRunner{})
		if err := app.Run(context.Background(), []string{"completion", shell}); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		required := []string{"--help", "--version", "--github", "--gitlab", "--update", "--all", "--description", "--public", "completion", "bash", "zsh", "fish"}
		if shell == "fish" {
			required = []string{"-l help", "-l version", "-l github", "-l gitlab", "-l update", "-l all", "-l description", "-l public", "completion", "bash", "zsh", "fish"}
		}
		for _, required := range required {
			if !strings.Contains(text, required) {
				t.Errorf("%s completion omits %s", shell, required)
			}
		}
		for _, forbidden := range []string{"status", "sync", "push", "visibility"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s advertises %s", shell, forbidden)
			}
		}
		checkedIn, err := os.ReadFile(filepath.Join("..", "..", "completions", completionFiles[shell]))
		if err != nil {
			t.Fatal(err)
		}
		if string(checkedIn) != text {
			t.Errorf("%s completion is stale; run scripts/generate-completions.sh", shell)
		}
	}
}

func TestCIWorkflowContract(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "test.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, required := range []string{"ubuntu-latest", "macos-latest", "archlinux:base-devel", "actions/checkout@v7", "actions/setup-go@v7", "go-version-file: go.mod", "gofmt -l", "Test, including completion drift", "go test -count=1", "go vet ./...", "go test -race -count=1", "goreleaser/goreleaser-action@v6", "version: '~> v2'", "args: check"} {
		if !strings.Contains(text, required) {
			t.Errorf("CI workflow omits %q", required)
		}
	}
	if strings.Contains(text, "generate-completions.sh") {
		t.Error("CI must rely on the Go drift test without mutating the checkout")
	}
}

func TestReleaseWorkflowPublishesTaggedArtifacts(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, required := range []string{"tags:", "'v*'", "contents: write", "actions/checkout@v7", "actions/setup-go@v7", "go-version-file: go.mod", "goreleaser-action@v6", "version: '~> v2'", "release --clean", "actions/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3", "PACKAGES_DISPATCH_TOKEN", "getReleaseByTag", "createDispatchEvent", "repo: 'packages'", "event_type: 'snip-release-published'", "source_commit: context.sha", "release_id: String(release.id)", "checksums_asset_id: String(checksums.id)", "checksums_digest: checksums.digest", "checksums.txt", ".deb", ".pkg.tar.zst"} {
		if !strings.Contains(text, required) {
			t.Errorf("release workflow omits %q", required)
		}
	}
	if strings.Index(text, "goreleaser-action@v6") > strings.Index(text, "createDispatchEvent") {
		t.Error("packages dispatch must happen only after GoReleaser succeeds")
	}
	releaseConfig, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"darwin", "linux", "amd64", "arm64", "-trimpath", "checksums.txt", "deb", "archlinux", "maintainer: Kris Wawrzyniak <wawrz.dev@gmail.com>", "dependencies:", "github-cli", "completions/*", "/usr/share/bash-completion/completions/snip", "/usr/share/zsh/site-functions/_snip", "/usr/share/fish/vendor_completions.d/snip.fish"} {
		if !strings.Contains(string(releaseConfig), required) {
			t.Errorf("release config omits %q", required)
		}
	}
}

type memoryIntentStore struct {
	mu         sync.Mutex
	intents    map[string]creationIntent
	saves      int
	failSaveAt int
}

func (s *memoryIntentStore) Load(key string) (creationIntent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.intents[key]
	return value, ok, nil
}
func (s *memoryIntentStore) Save(intent creationIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if s.saves == s.failSaveAt {
		return errors.New("disk full")
	}
	if s.intents == nil {
		s.intents = map[string]creationIntent{}
	}
	s.intents[intent.Key] = intent
	return nil
}
func (s *memoryIntentStore) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.intents, key)
	return nil
}
func (s *memoryIntentStore) Path(key string) string { return "/state/pending/" + key + ".json" }

func creationRunner(createCalls *int) *fakeRunner {
	return &fakeRunner{outputFn: func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "gh" && strings.HasPrefix(joined, "auth status"):
			return []byte("ok"), nil
		case name == "gh" && joined == "api --hostname github.com user":
			return []byte(`{"login":"alice"}`), nil
		case name == "gh" && strings.HasPrefix(joined, "gist create"):
			*createCalls++
			return []byte("https://gist.github.com/alice/cafebabe1234\n"), nil
		default:
			return nil, fmt.Errorf("unexpected %s %s", name, joined)
		}
	}}
}

func TestIntentMustPersistBeforeProviderMutation(t *testing.T) {
	createCalls := 0
	runner := creationRunner(&createCalls)
	app, _, _ := newTestApp(t, runner)
	app.Intents = &memoryIntentStore{failSaveAt: 1}
	file := filepath.Join(t.TempDir(), "demo.txt")
	_ = os.WriteFile(file, []byte("hello"), 0o600)
	err := app.Run(context.Background(), []string{"file", file})
	if err == nil || !strings.Contains(err.Error(), "before remote write") {
		t.Fatalf("unexpected error: %v", err)
	}
	if createCalls != 0 {
		t.Fatal("provider mutated without durable intent")
	}
}

func TestUnrecordableProviderResultBlocksDuplicateCreation(t *testing.T) {
	createCalls := 0
	runner := creationRunner(&createCalls)
	store := &memoryIntentStore{failSaveAt: 3}
	app, _, _ := newTestApp(t, runner)
	app.Intents = store
	file := filepath.Join(t.TempDir(), "demo.txt")
	_ = os.WriteFile(file, []byte("hello"), 0o600)
	args := []string{"file", file}
	if err := app.Run(context.Background(), args); err == nil || !strings.Contains(err.Error(), "requires recovery") {
		t.Fatalf("first call: %v", err)
	}
	if err := app.Run(context.Background(), args); err == nil || !strings.Contains(err.Error(), "/state/pending/") {
		t.Fatalf("retry: %v", err)
	}
	if createCalls != 1 {
		t.Fatalf("created %d remotes", createCalls)
	}
}

func TestCreatedNewIntentRetriesWithoutReopeningEditor(t *testing.T) {
	createCalls, editorCalls := 0, 0
	runner := creationRunner(&createCalls)
	runner.outputFn = func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "git" && strings.Contains(joined, "rev-parse") {
			return []byte("true\n"), nil
		}
		if name == "git" && strings.Contains(joined, "remote get-url") {
			return []byte("https://gist.github.com/cafebabe1234.git\n"), nil
		}
		return githubFixture(name, args)
	}
	runner.runFn = func(_ io.Reader, _, _ io.Writer, name string, args []string) error {
		if name == "fake-editor" {
			editorCalls++
			return nil
		}
		if name == "git" && args[0] == "clone" {
			return os.MkdirAll(filepath.Join(args[len(args)-1], ".git"), 0o700)
		}
		return fmt.Errorf("unexpected run %s %v", name, args)
	}
	app, _, _ := newTestApp(t, runner)
	t.Setenv("EDITOR", "fake-editor")
	source := Source{Provider: "github", Host: "github.com", Account: "alice"}
	key := creationKey(source, "new", "demo.txt", "", false)
	store := &memoryIntentStore{intents: map[string]creationIntent{key: {Version: 1, Key: key, Status: "created", Operation: "new", Source: source, Request: CreateRequest{Filename: "demo.txt", Content: []byte("saved editor content")}, Item: Item{Source: source, ID: "cafebabe1234", Files: []string{"demo.txt"}, URL: "https://gist.github.com/alice/cafebabe1234", CloneURL: "https://gist.github.com/cafebabe1234.git"}}}}
	app.Intents = store
	if err := app.Run(context.Background(), []string{"new", "demo.txt"}); err != nil {
		t.Fatal(err)
	}
	if editorCalls != 0 || createCalls != 0 {
		t.Fatalf("retry reopened editor (%d) or created remote (%d)", editorCalls, createCalls)
	}
}

func TestPreparedNewIntentUsesDurableCapturedContent(t *testing.T) {
	createCalls, editorCalls := 0, 0
	runner := creationRunner(&createCalls)
	providerOutput := runner.outputFn
	runner.outputFn = func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "git" && strings.Contains(joined, "rev-parse") {
			return []byte("true\n"), nil
		}
		if name == "git" && strings.Contains(joined, "remote get-url") {
			return []byte("https://gist.github.com/cafebabe1234.git\n"), nil
		}
		return providerOutput(name, args)
	}
	runner.runFn = func(_ io.Reader, _, _ io.Writer, name string, args []string) error {
		if name == "fake-editor" {
			editorCalls++
			return nil
		}
		if name == "git" && args[0] == "clone" {
			return os.MkdirAll(filepath.Join(args[len(args)-1], ".git"), 0o700)
		}
		return fmt.Errorf("unexpected run %s %v", name, args)
	}
	app, _, _ := newTestApp(t, runner)
	t.Setenv("EDITOR", "fake-editor")
	source := Source{Provider: "github", Host: "github.com", Account: "alice"}
	key := creationKey(source, "new", "demo.txt", "", false)
	store := &memoryIntentStore{intents: map[string]creationIntent{key: {Version: 1, Key: key, Status: "prepared", Operation: "new", Source: source, Request: CreateRequest{Filename: "demo.txt", Content: []byte("saved editor content")}}}}
	app.Intents = store
	if err := app.Run(context.Background(), []string{"new", "demo.txt"}); err != nil {
		t.Fatal(err)
	}
	if editorCalls != 0 || createCalls != 1 {
		t.Fatalf("prepared retry reopened editor (%d) or created %d remotes", editorCalls, createCalls)
	}
}

func TestProviderFailureLeavesRecoveryBarrier(t *testing.T) {
	createCalls := 0
	runner := creationRunner(&createCalls)
	runner.outputFn = func(name string, args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "gh" && strings.HasPrefix(joined, "gist create") {
			createCalls++
			return nil, errors.New("connection lost after request")
		}
		return githubFixture(name, args)
	}
	store := &memoryIntentStore{}
	app, _, _ := newTestApp(t, runner)
	app.Intents = store
	file := filepath.Join(t.TempDir(), "demo.txt")
	_ = os.WriteFile(file, []byte("hello"), 0o600)
	args := []string{"file", file}
	if err := app.Run(context.Background(), args); err == nil || !strings.Contains(err.Error(), "requires recovery") {
		t.Fatalf("first call: %v", err)
	}
	if err := app.Run(context.Background(), args); err == nil || !strings.Contains(err.Error(), "requires recovery") {
		t.Fatalf("retry: %v", err)
	}
	if createCalls != 1 {
		t.Fatalf("created %d times", createCalls)
	}
}
