package snip

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const metadataName = "snip.json"

type cloneMetadata struct {
	Version     int      `json:"version"`
	Provider    string   `json:"provider"`
	Host        string   `json:"host"`
	Account     string   `json:"account"`
	ID          string   `json:"id"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Files       []string `json:"files,omitempty"`
	URL         string   `json:"url,omitempty"`
	CloneURL    string   `json:"clone_url"`
}

func metadataPath(clonePath string) string { return filepath.Join(clonePath, ".git", metadataName) }

func metadataFromItem(item Item) cloneMetadata {
	return cloneMetadata{Version: 1, Provider: item.Provider, Host: item.Host, Account: item.Account, ID: item.ID, Title: item.Title, Description: item.Description, Files: item.Files, URL: item.URL, CloneURL: item.CloneURL}
}

func (m cloneMetadata) item(path string) Item {
	return Item{Source: Source{Provider: m.Provider, Host: m.Host, Account: m.Account}, ID: m.ID, Title: m.Title, Description: m.Description, Files: m.Files, URL: m.URL, CloneURL: m.CloneURL, LocalPath: path}
}

func writeMetadata(path string, item Item) error {
	data, err := json.MarshalIndent(metadataFromItem(item), "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWrite(metadataPath(path), data, 0o600)
}

func readMetadata(path string) (Item, error) {
	data, err := os.ReadFile(metadataPath(path))
	if err != nil {
		return Item{}, err
	}
	var metadata cloneMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Item{}, err
	}
	if metadata.Version != 1 || metadata.Provider == "" || metadata.Host == "" || metadata.Account == "" || metadata.ID == "" {
		return Item{}, errors.New("incomplete snip clone metadata")
	}
	return metadata.item(path), nil
}

func scanLocal(root string, eligible map[string]string) ([]Item, error) {
	hosts, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, hostEntry := range hosts {
		if !hostEntry.IsDir() {
			continue
		}
		hostPath := filepath.Join(root, hostEntry.Name())
		accounts, err := os.ReadDir(hostPath)
		if err != nil {
			return nil, err
		}
		for _, accountEntry := range accounts {
			if !accountEntry.IsDir() {
				continue
			}
			accountPath := filepath.Join(hostPath, accountEntry.Name())
			clones, err := os.ReadDir(accountPath)
			if err != nil {
				return nil, err
			}
			for _, cloneEntry := range clones {
				if !cloneEntry.IsDir() {
					continue
				}
				path := filepath.Join(accountPath, cloneEntry.Name())
				item, err := readMetadata(path)
				if err != nil {
					continue
				}
				provider, ok := eligible[strings.ToLower(item.Host)]
				if !ok || provider != item.Provider {
					continue
				}
				if safeComponent(item.Host) != hostEntry.Name() || safeComponent(item.Account) != accountEntry.Name() {
					continue
				}
				items = append(items, item)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return itemKey(items[i]) < itemKey(items[j]) })
	return items, nil
}

func itemKey(item Item) string {
	return strings.Join([]string{item.Provider, strings.ToLower(item.Host), strings.ToLower(item.Account), item.ID}, "\x00")
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".snip-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}
