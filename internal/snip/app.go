package snip

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type App struct {
	In         io.Reader
	Out        io.Writer
	Err        io.Writer
	Runner     Runner
	Version    string
	IsTTY      func() bool
	LookPath   func(string) (string, error)
	LoadConfig func() (Config, error)
	Intents    intentStore
}

func NewApp(in io.Reader, out, errOut io.Writer) *App {
	app := &App{
		In: in, Out: out, Err: errOut, Runner: execRunner{}, Version: "dev",
		IsTTY: func() bool {
			inFile, inOK := in.(*os.File)
			if !inOK {
				return false
			}
			inInfo, inErr := inFile.Stat()
			return inErr == nil && inInfo.Mode()&os.ModeCharDevice != 0
		},
		LookPath:   exec.LookPath,
		LoadConfig: loadConfig,
	}
	app.Intents, _ = defaultIntentStore()
	return app
}

const usage = `snip manages GitHub gists and account-level GitLab snippets.

Usage:
  snip [--github|--gitlab] [query]
  snip list [--github|--gitlab]
  snip get [--github|--gitlab] [--update] <name|url|id>
  snip get [--github|--gitlab] [--update] --all
  snip new [--github|--gitlab] [--description text] [--public] <filename>
  snip paste [--github|--gitlab] [--description text] [--public] <filename>
  snip file [--github|--gitlab] [--description text] [--public] <path>
  snip web [--github|--gitlab] [query]
  snip completion <bash|zsh|fish>
  snip version

Bare and query forms use the interactive fzf picker when needed. Commands print
a verified clone path after get/select so a shell wrapper can change directory.
Use ordinary Git commands inside a clone for later edits and synchronization.
`

type globalOptions struct {
	provider string
	args     []string
}

func parseGlobal(args []string) (globalOptions, error) {
	var out globalOptions
	for _, arg := range args {
		switch arg {
		case "--github":
			if out.provider != "" && out.provider != "github" {
				return out, errors.New("--github and --gitlab are mutually exclusive")
			}
			out.provider = "github"
		case "--gitlab":
			if out.provider != "" && out.provider != "gitlab" {
				return out, errors.New("--github and --gitlab are mutually exclusive")
			}
			out.provider = "gitlab"
		default:
			out.args = append(out.args, arg)
		}
	}
	return out, nil
}

func (a *App) Run(ctx context.Context, args []string) error {
	global, err := parseGlobal(args)
	if err != nil {
		return err
	}
	args = global.args
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Fprint(a.Out, usage)
			return nil
		case "version", "--version":
			fmt.Fprintln(a.Out, a.Version)
			return nil
		case "completion":
			return a.completion(args[1:])
		case "status", "sync", "push", "visibility":
			return fmt.Errorf("unknown command %q; use ordinary Git commands inside a clone", args[0])
		}
	}
	cfg, err := a.LoadConfig()
	if err != nil {
		return err
	}
	providers := map[string]Provider{
		"github": cliProvider{kind: "github", runner: a.Runner},
		"gitlab": cliProvider{kind: "gitlab", runner: a.Runner},
	}
	command := "browse"
	rest := args
	if len(args) > 0 {
		switch args[0] {
		case "list", "get", "new", "paste", "file", "web":
			command, rest = args[0], args[1:]
		}
	}
	switch command {
	case "new", "paste", "file":
		return a.create(ctx, cfg, providers, global.provider, command, rest)
	case "list":
		if len(rest) != 0 {
			return errors.New("list accepts no arguments")
		}
		items, err := a.inventory(ctx, cfg, providers, global.provider)
		if err != nil {
			return err
		}
		writeList(a.Out, items)
		return nil
	case "get":
		return a.get(ctx, cfg, providers, global.provider, rest)
	case "web":
		if len(rest) > 1 {
			return errors.New("web accepts at most one query")
		}
		query := ""
		if len(rest) == 1 {
			query = rest[0]
		}
		return a.web(ctx, cfg, providers, global.provider, query)
	default:
		if len(rest) > 1 {
			return errors.New("expected at most one query")
		}
		query := ""
		if len(rest) == 1 {
			query = rest[0]
		}
		return a.browse(ctx, cfg, providers, global.provider, query)
	}
}

func (a *App) configured(cfg Config, selected string, one bool) ([]struct {
	name string
	cfg  providerConfig
}, error) {
	all := []struct {
		name string
		cfg  providerConfig
	}{{"github", cfg.GitHub}, {"gitlab", cfg.GitLab}}
	if selected == "" && one {
		selected = cfg.DefaultProvider
	}
	var result []struct {
		name string
		cfg  providerConfig
	}
	for _, entry := range all {
		if entry.cfg.Enabled && (selected == "" || selected == entry.name) {
			result = append(result, entry)
		}
	}
	if len(result) == 0 {
		if selected != "" {
			return nil, fmt.Errorf("%s source is not configured", selected)
		}
		return nil, errors.New("no snippet source is configured")
	}
	if one && len(result) != 1 {
		return nil, errors.New("source is ambiguous; pass --github or --gitlab")
	}
	return result, nil
}

func (a *App) resolveSources(ctx context.Context, cfg Config, providers map[string]Provider, selected string, one bool) ([]Source, error) {
	configured, err := a.configured(cfg, selected, one)
	if err != nil {
		return nil, err
	}
	var sources []Source
	for _, entry := range configured {
		source, err := providers[entry.name].Source(ctx, entry.cfg)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func (a *App) root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "snip"), nil
}

func (a *App) inventory(ctx context.Context, cfg Config, providers map[string]Provider, selected string) ([]Item, error) {
	configured, err := a.configured(cfg, selected, false)
	if err != nil {
		return nil, err
	}
	root, err := a.root()
	if err != nil {
		return nil, err
	}
	eligible := make(map[string]string, len(configured))
	for _, entry := range configured {
		eligible[strings.ToLower(entry.cfg.Host)] = entry.name
	}
	locals, err := scanLocal(root, eligible)
	if err != nil {
		return nil, err
	}
	localByID := make(map[string]Item, len(locals))
	for _, local := range locals {
		localByID[itemKey(local)] = local
	}
	var inventory []Item
	onlineAccounts := map[string]string{}
	offlineHosts := map[string]bool{}
	for _, entry := range configured {
		source, sourceErr := providers[entry.name].Source(ctx, entry.cfg)
		if sourceErr != nil {
			var auth authError
			if errors.As(sourceErr, &auth) {
				return nil, sourceErr
			}
			offlineHosts[strings.ToLower(entry.cfg.Host)] = true
			fmt.Fprintf(a.Err, "snip: %s unavailable; showing local clones only\n", entry.cfg.Host)
			continue
		}
		onlineAccounts[strings.ToLower(source.Host)] = source.Account
		items, listErr := providers[source.Provider].List(ctx, source)
		if listErr != nil {
			if looksLikeAuthFailure(listErr) {
				return nil, providerAuthError(source.Provider, source.Host)
			}
			fmt.Fprintf(a.Err, "snip: %s/%s unavailable; showing local clones only\n", source.Provider, source.Account)
			offlineHosts[strings.ToLower(source.Host)] = true
			continue
		}
		for i := range items {
			if local, ok := localByID[itemKey(items[i])]; ok {
				items[i].LocalPath = local.LocalPath
				delete(localByID, itemKey(items[i]))
			}
		}
		inventory = append(inventory, items...)
	}
	for _, local := range localByID {
		if offlineHosts[strings.ToLower(local.Host)] || strings.EqualFold(onlineAccounts[strings.ToLower(local.Host)], local.Account) {
			inventory = append(inventory, local)
		}
	}
	sort.Slice(inventory, func(i, j int) bool {
		if inventory[i].Source.Key() != inventory[j].Source.Key() {
			return inventory[i].Source.Key() < inventory[j].Source.Key()
		}
		return inventory[i].ID < inventory[j].ID
	})
	return inventory, nil
}

func writeList(w io.Writer, items []Item) {
	for _, item := range items {
		cloned := "no"
		if item.LocalPath != "" {
			cloned = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", safeField(item.Provider), safeField(item.Host), safeField(item.Account), safeField(item.ID), cloned, safeField(item.Name()), safeField(strings.Join(item.Files, ",")), safeField(item.URL))
	}
}

func safeField(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
}

func matchText(item Item) string {
	return strings.ToLower(strings.Join([]string{item.ID, item.Title, item.Description, strings.Join(item.Files, " "), item.Host, item.Account}, " "))
}

func exact(item Item, query string) bool {
	q := strings.ToLower(query)
	if strings.ToLower(item.ID) == q || strings.ToLower(item.Title) == q || strings.ToLower(item.Description) == q {
		return true
	}
	for _, f := range item.Files {
		if strings.ToLower(f) == q || strings.ToLower(strings.TrimSuffix(f, filepath.Ext(f))) == q {
			return true
		}
	}
	return false
}

func (a *App) choose(ctx context.Context, items []Item, query string, exactOnly bool) (Item, error) {
	if len(items) == 0 {
		return Item{}, errors.New("no snippets found")
	}
	var exactMatches, partial []Item
	for _, item := range items {
		if query == "" || strings.Contains(matchText(item), strings.ToLower(query)) {
			partial = append(partial, item)
		}
		if query != "" && exact(item, query) {
			exactMatches = append(exactMatches, item)
		}
	}
	if query != "" {
		if len(exactMatches) == 1 {
			return exactMatches[0], nil
		}
		if !exactOnly && len(partial) == 1 {
			return partial[0], nil
		}
	}
	if !a.IsTTY() {
		if len(exactMatches) > 1 {
			return Item{}, fmt.Errorf("%q matches multiple snippets; pass a provider flag or use a TTY", query)
		}
		return Item{}, fmt.Errorf("%q does not resolve uniquely and an interactive picker is unavailable", query)
	}
	return a.fzf(ctx, items, query)
}

func (a *App) fzf(ctx context.Context, items []Item, query string) (Item, error) {
	var input bytes.Buffer
	for i, item := range items {
		status := "remote"
		if item.LocalPath != "" {
			status = "cloned"
		}
		label := fmt.Sprintf("%s  %s/%s  %s  [%s]  %s", item.Provider, item.Host, item.Account, item.Name(), status, strings.Join(item.Files, ", "))
		fmt.Fprintf(&input, "%d\t%s\n", i, safeField(label))
	}
	var output bytes.Buffer
	args := []string{"--delimiter=\t", "--with-nth=2", "--prompt=snip> ", "--no-multi"}
	if query != "" {
		args = append(args, "--query", query)
	}
	if err := a.Runner.Run(ctx, &input, &output, a.Err, "fzf", args...); err != nil {
		return Item{}, errors.New("selection cancelled")
	}
	selected := strings.TrimSuffix(output.String(), "\n")
	fields := strings.SplitN(selected, "\t", 2)
	index, parseErr := strconv.Atoi(fields[0])
	if parseErr != nil || len(fields) != 2 || index < 0 || index >= len(items) {
		return Item{}, errors.New("fzf returned an invalid selection")
	}
	return items[index], nil
}

func (a *App) browse(ctx context.Context, cfg Config, providers map[string]Provider, selected, query string) error {
	items, err := a.inventory(ctx, cfg, providers, selected)
	if err != nil {
		return err
	}
	item, err := a.choose(ctx, items, query, false)
	if err != nil {
		return err
	}
	path, err := a.clone(ctx, item, false)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Out, path)
	return nil
}

func (a *App) get(ctx context.Context, cfg Config, providers map[string]Provider, selected string, args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	update := fs.Bool("update", false, "update existing clones")
	all := fs.Bool("all", false, "operate on every item in one source")
	if err := fs.Parse(flagsBeforeArgs(args, map[string]bool{"--update": false, "--all": false})); err != nil {
		return err
	}
	if *all {
		if fs.NArg() != 0 {
			return errors.New("get --all accepts no name")
		}
		sources, err := a.resolveSources(ctx, cfg, providers, selected, true)
		if err != nil {
			return err
		}
		items, err := providers[sources[0].Provider].List(ctx, sources[0])
		if err != nil {
			return err
		}
		var failures []string
		for _, item := range items {
			path, cloneErr := a.clone(ctx, item, *update)
			if cloneErr != nil {
				failures = append(failures, item.ID+": "+cloneErr.Error())
				continue
			}
			fmt.Fprintln(a.Out, path)
		}
		if len(failures) > 0 {
			return fmt.Errorf("some clones failed: %s", strings.Join(failures, "; "))
		}
		return nil
	}
	if fs.NArg() != 1 {
		return errors.New("get requires one name, URL, or provider ID")
	}
	query := fs.Arg(0)
	explicitProvider, explicitHost, explicitID := parseExplicitID(query)
	if selected != "" && explicitHost == "" {
		if selected == "github" && len(query) >= 8 && allHex(query) {
			explicitProvider, explicitID = "github", query
		}
		if selected == "gitlab" {
			if _, parseErr := strconv.ParseUint(query, 10, 64); parseErr == nil {
				explicitProvider, explicitID = "gitlab", query
			}
		}
	}
	if explicitProvider != "" {
		if selected != "" && selected != explicitProvider {
			return fmt.Errorf("%q conflicts with --%s", query, selected)
		}
		configured, err := a.configured(cfg, explicitProvider, true)
		if err != nil {
			return err
		}
		if explicitHost != "" && !strings.EqualFold(explicitHost, configured[0].cfg.Host) {
			return fmt.Errorf("%s URL host %q does not match configured host %q", explicitProvider, explicitHost, configured[0].cfg.Host)
		}
		items, inventoryErr := a.inventory(ctx, cfg, providers, explicitProvider)
		if inventoryErr != nil {
			return inventoryErr
		}
		var matches []Item
		for _, candidate := range items {
			if candidate.ID == explicitID {
				matches = append(matches, candidate)
			}
		}
		if len(matches) != 1 {
			return fmt.Errorf("%s item %q not found", explicitProvider, explicitID)
		}
		item := matches[0]
		if item.LocalPath == "" && item.CloneURL == "" {
			return fmt.Errorf("%s item %q is unavailable offline", explicitProvider, explicitID)
		}
		path, err := a.clone(ctx, item, *update)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.Out, path)
		return nil
	}
	items, err := a.inventory(ctx, cfg, providers, selected)
	if err != nil {
		return err
	}
	item, err := a.choose(ctx, items, query, true)
	if err != nil {
		return err
	}
	path, err := a.clone(ctx, item, *update)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Out, path)
	return nil
}

func (a *App) clone(ctx context.Context, item Item, update bool) (string, error) {
	root, err := a.root()
	if err != nil {
		return "", err
	}
	if item.LocalPath == "" {
		item.LocalPath = findExisting(root, item)
	}
	if item.LocalPath == "" {
		item.LocalPath, err = a.availablePath(ctx, root, item)
		if err != nil {
			return "", err
		}
		if _, statErr := os.Stat(item.LocalPath); statErr == nil {
			if err := a.verifyLocal(ctx, item.LocalPath, item); err != nil {
				return "", err
			}
			if update {
				if err := a.Runner.Run(ctx, nil, a.Err, a.Err, "git", "-C", item.LocalPath, "pull", "--ff-only"); err != nil {
					return "", fmt.Errorf("update %s (local changes were preserved; resolve any conflict in the clone): %w", item.LocalPath, err)
				}
			}
			return item.LocalPath, nil
		}
		if item.CloneURL == "" {
			return "", fmt.Errorf("clone URL unavailable for %s/%s", item.Provider, item.ID)
		}
		if err := os.MkdirAll(filepath.Dir(item.LocalPath), 0o700); err != nil {
			return "", err
		}
		if err := a.Runner.Run(ctx, nil, a.Err, a.Err, "git", "clone", "--", item.CloneURL, item.LocalPath); err != nil {
			return "", fmt.Errorf("clone %s: %w", item.URL, err)
		}
	}
	if err := a.verifyLocal(ctx, item.LocalPath, item); err != nil {
		return "", err
	}
	if update {
		if err := a.Runner.Run(ctx, nil, a.Err, a.Err, "git", "-C", item.LocalPath, "pull", "--ff-only"); err != nil {
			return "", fmt.Errorf("update %s (local changes were preserved; resolve any conflict in the clone): %w", item.LocalPath, err)
		}
	}
	if err := writeMetadata(item.LocalPath, item); err != nil {
		return "", fmt.Errorf("record clone identity: %w", err)
	}
	return item.LocalPath, nil
}

func findExisting(root string, item Item) string {
	dir := filepath.Join(root, safeComponent(item.Host), safeComponent(item.Account))
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		local, err := readMetadata(path)
		if err == nil && itemKey(local) == itemKey(item) {
			return path
		}
	}
	return ""
}

func (a *App) availablePath(ctx context.Context, root string, item Item) (string, error) {
	dir := filepath.Join(root, safeComponent(item.Host), safeComponent(item.Account))
	base := filepath.Join(dir, slug(item.Name()))
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return base, nil
	}
	if err := a.verifyLocal(ctx, base, item); err == nil {
		return base, nil
	}
	collision := base + "-" + safeID(item.ID)
	if _, err := os.Stat(collision); errors.Is(err, os.ErrNotExist) {
		return collision, nil
	}
	if err := a.verifyLocal(ctx, collision, item); err == nil {
		return collision, nil
	}
	return "", fmt.Errorf("clone path collision at %s", collision)
}

func (a *App) verifyLocal(ctx context.Context, path string, item Item) error {
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a verified Git clone", path)
	}
	verified, err := a.Runner.Output(ctx, "git", "-C", path, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(verified)) != "true" {
		return fmt.Errorf("%s is not a verified Git clone", path)
	}
	origin, err := a.Runner.Output(ctx, "git", "-C", path, "remote", "get-url", "origin")
	if err != nil {
		return fmt.Errorf("verify origin for %s: %w", path, err)
	}
	provider, host, id := remoteIdentity(strings.TrimSpace(string(origin)))
	if provider != item.Provider || !strings.EqualFold(host, item.Host) || id != item.ID {
		return fmt.Errorf("%s origin does not match %s/%s/%s", path, item.Provider, item.Host, item.ID)
	}
	if local, err := readMetadata(path); err == nil && itemKey(local) != itemKey(item) {
		return fmt.Errorf("%s metadata belongs to another remote", path)
	}
	if err := writeMetadata(path, item); err != nil {
		return fmt.Errorf("record clone identity: %w", err)
	}
	return nil
}

func remoteIdentity(remote string) (provider, host, id string) {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(remote, "/"), ".git")
	remoteHost, path := "", ""
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Hostname() != "" {
		remoteHost, path = strings.ToLower(parsed.Hostname()), parsed.Path
	} else if at := strings.LastIndex(trimmed, "@"); at >= 0 {
		rest := trimmed[at+1:]
		if colon := strings.Index(rest, ":"); colon >= 0 {
			remoteHost, path = strings.ToLower(rest[:colon]), rest[colon+1:]
		}
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if remoteHost == "gist.github.com" && len(parts) > 0 {
		return "github", "github.com", parts[len(parts)-1]
	}
	for i, part := range parts {
		if part == "snippets" && i+1 < len(parts) {
			return "gitlab", remoteHost, parts[i+1]
		}
	}
	return "", remoteHost, ""
}

func safeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func safeComponent(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	dash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	result := strings.Trim(b.String(), ".-")
	if result == "" || result == "." || result == ".." {
		return "unknown"
	}
	return result
}

func slug(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	dash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
		if b.Len() >= 64 {
			break
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "snippet"
	}
	return result
}

func (a *App) create(ctx context.Context, cfg Config, providers map[string]Provider, selected, kind string, args []string) error {
	fs := flag.NewFlagSet(kind, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	description := fs.String("description", "", "description or title")
	public := fs.Bool("public", false, "make public")
	if err := fs.Parse(flagsBeforeArgs(args, map[string]bool{"--description": true, "--public": false})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("%s requires one filename or path", kind)
	}
	sources, err := a.resolveSources(ctx, cfg, providers, selected, true)
	if err != nil {
		return err
	}
	source := sources[0]
	filename := fs.Arg(0)
	if kind == "file" {
		filename = filepath.Base(filename)
	}
	if err := validateFilename(filename); err != nil {
		return err
	}
	if a.Intents == nil {
		a.Intents, err = defaultIntentStore()
		if err != nil {
			return err
		}
	}
	key := creationKey(source, kind, filename, *description, *public)
	intent, exists, err := a.Intents.Load(key)
	if err != nil {
		return err
	}
	if exists && intent.Status == "creating" {
		return recoveryError(a.Intents, key, nil)
	}
	if exists && intent.Status != "prepared" && intent.Status != "created" {
		return recoveryError(a.Intents, key, errors.New("unknown intent state"))
	}
	if !exists {
		var content []byte
		switch kind {
		case "new":
			content, err = a.edit(ctx, filename)
		case "paste":
			content, err = a.clipboard(ctx)
		case "file":
			info, statErr := os.Stat(fs.Arg(0))
			if statErr != nil {
				return statErr
			}
			if !info.Mode().IsRegular() {
				return errors.New("file requires one readable regular file")
			}
			content, err = os.ReadFile(fs.Arg(0))
		}
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(content)) == 0 {
			return errors.New("content is empty; nothing was created")
		}
		intent = creationIntent{Version: 1, Key: key, Status: "prepared", Operation: kind, Source: source, Request: CreateRequest{Filename: filename, Description: *description, Content: content, Public: *public}}
		if err := a.Intents.Save(intent); err != nil {
			return fmt.Errorf("save creation intent before remote write: %w", err)
		}
	}
	item := intent.Item
	if intent.Status == "prepared" {
		visibility := "secret/unlisted"
		if source.Provider == "gitlab" {
			visibility = "private"
		}
		if *public {
			visibility = "public"
		}
		fmt.Fprintf(a.Err, "Creating %s item on %s as %s (%s)\n", source.Provider, source.Host, source.Account, visibility)
		intent.Status = "creating"
		if err := a.Intents.Save(intent); err != nil {
			return fmt.Errorf("mark creation in progress: %w", err)
		}
		item, err = providers[source.Provider].Create(ctx, source, intent.Request)
		if err != nil {
			return recoveryError(a.Intents, key, err)
		}
		intent.Status, intent.Item = "created", item
		if err := a.Intents.Save(intent); err != nil {
			return recoveryError(a.Intents, key, fmt.Errorf("remote returned %s but identity could not be recorded: %w", item.URL, err))
		}
	} else {
		fmt.Fprintf(a.Err, "Retrying clone of existing remote %s\n", item.URL)
	}
	path, err := a.clone(ctx, item, false)
	if err != nil {
		return fmt.Errorf("remote retained at %s; rerun the same command to retry: %w", item.URL, err)
	}
	if err := a.Intents.Remove(key); err != nil {
		return fmt.Errorf("clone verified at %s but creation intent could not be cleared: %w", path, err)
	}
	fmt.Fprintf(a.Out, "%s\n%s\n", item.URL, path)
	return nil
}

func flagsBeforeArgs(args []string, known map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name := arg
		if cut := strings.IndexByte(arg, '='); cut >= 0 {
			name = arg[:cut]
		}
		needsValue, ok := known[name]
		if !ok {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		if needsValue && name == arg && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func validateFilename(name string) error {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00\r\n") {
		return fmt.Errorf("unsafe filename %q; use a basename without path separators", name)
	}
	return nil
}

func (a *App) edit(ctx context.Context, filename string) ([]byte, error) {
	tmp, err := tempFileWithName(filename, nil)
	if err != nil {
		return nil, err
	}
	defer tmp.cleanup()
	editor := strings.Fields(os.Getenv("EDITOR"))
	if len(editor) == 0 {
		return nil, errors.New("EDITOR is not set")
	}
	if err := a.Runner.Run(ctx, a.In, a.Out, a.Err, editor[0], append(editor[1:], tmp.path)...); err != nil {
		return nil, fmt.Errorf("editor failed: %w", err)
	}
	return os.ReadFile(tmp.path)
}

func (a *App) clipboard(ctx context.Context) ([]byte, error) {
	commands := [][]string{{"pbpaste"}, {"wl-paste", "--no-newline"}, {"xclip", "-selection", "clipboard", "-o"}}
	for _, command := range commands {
		if _, err := a.LookPath(command[0]); err != nil {
			continue
		}
		out, err := a.Runner.Output(ctx, command[0], command[1:]...)
		if err != nil {
			return nil, fmt.Errorf("read clipboard: %w", err)
		}
		return out, nil
	}
	return nil, errors.New("no supported clipboard command found (pbpaste, wl-paste, or xclip)")
}

func (a *App) web(ctx context.Context, cfg Config, providers map[string]Provider, selected, query string) error {
	items, err := a.inventory(ctx, cfg, providers, selected)
	if err != nil {
		return err
	}
	item, err := a.choose(ctx, items, query, false)
	if err != nil {
		return err
	}
	if item.URL == "" {
		return errors.New("provider URL unavailable while offline")
	}
	browser := strings.Fields(os.Getenv("BROWSER"))
	if len(browser) == 0 {
		if runtime.GOOS == "darwin" {
			browser = []string{"open"}
		} else {
			browser = []string{"xdg-open"}
		}
	}
	return a.Runner.Run(ctx, nil, a.Out, a.Err, browser[0], append(browser[1:], item.URL)...)
}
