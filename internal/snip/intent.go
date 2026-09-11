package snip

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type creationIntent struct {
	Version   int           `json:"version"`
	Key       string        `json:"key"`
	Status    string        `json:"status"`
	Operation string        `json:"operation"`
	Source    Source        `json:"source"`
	Request   CreateRequest `json:"request"`
	Item      Item          `json:"item,omitempty"`
}

type intentStore interface {
	Load(string) (creationIntent, bool, error)
	Save(creationIntent) error
	Remove(string) error
	Path(string) string
}

type fileIntentStore struct{ dir string }

func defaultIntentStore() (intentStore, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return fileIntentStore{dir: filepath.Join(dir, "snip", "pending")}, nil
}

func (s fileIntentStore) Path(key string) string { return filepath.Join(s.dir, key+".json") }

func (s fileIntentStore) Load(key string) (creationIntent, bool, error) {
	data, err := os.ReadFile(s.Path(key))
	if errors.Is(err, os.ErrNotExist) {
		return creationIntent{}, false, nil
	}
	if err != nil {
		return creationIntent{}, false, err
	}
	var intent creationIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		return creationIntent{}, false, fmt.Errorf("read creation intent: %w", err)
	}
	if intent.Version != 1 || intent.Key != key {
		return creationIntent{}, false, errors.New("invalid creation intent")
	}
	return intent, true, nil
}

func (s fileIntentStore) Save(intent creationIntent) error {
	data, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.Path(intent.Key), append(data, '\n'), 0o600)
}

func (s fileIntentStore) Remove(key string) error {
	err := os.Remove(s.Path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func creationKey(source Source, operation, filename, description string, public bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t", source.Provider, source.Host, source.Account, operation, filename, description, public)
	return hex.EncodeToString(h.Sum(nil))
}

func recoveryError(store intentStore, key string, cause error) error {
	message := fmt.Sprintf("creation outcome requires recovery; inspect the provider and %s before removing or repairing the intent", store.Path(key))
	if cause != nil {
		message += ": " + cause.Error()
	}
	return errors.New(message)
}
