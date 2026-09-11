// Package session stores conversations as JSON files in the data directory.
package session

import (
	"cmp"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"minillm/internal/config"
	"minillm/internal/llm"
)

type Session struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Model    string        `json:"model"`
	Mode     string        `json:"mode"`
	System   string        `json:"system,omitempty"`
	Created  time.Time     `json:"created"`
	Updated  time.Time     `json:"updated"`
	Messages []llm.Message `json:"messages"`
}

// Meta is what the session picker shows.
type Meta struct {
	ID, Title, Model string
	Updated          time.Time
}

func dir() string { return filepath.Join(config.DataDir(), "sessions") }

func New() *Session {
	now := time.Now()
	return &Session{ID: now.Format("20060102-150405.000"), Created: now, Mode: "chat"}
}

// Save writes the session; empty sessions are not saved.
func (s *Session) Save() error {
	if len(s.Messages) == 0 {
		return nil
	}
	if s.Title == "" {
		s.Title = title(s.Messages)
	}
	s.Updated = time.Now()
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir(), s.ID+".json")
	if err := os.WriteFile(path+".tmp", raw, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func Load(id string) (*Session, error) {
	raw, err := os.ReadFile(filepath.Join(dir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Latest returns the most recently updated session, or os.ErrNotExist.
func Latest() (*Session, error) {
	metas, err := List(1)
	if err != nil {
		return nil, err
	}
	if len(metas) == 0 {
		return nil, os.ErrNotExist
	}
	return Load(metas[0].ID)
}

// List returns up to limit sessions, most recently updated first.
func List(limit int) ([]Meta, error) {
	entries, err := os.ReadDir(dir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type file struct {
		id  string
		mod time.Time
	}
	var files []file
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, file{id, info.ModTime()})
		}
	}
	slices.SortFunc(files, func(a, b file) int { return cmp.Compare(b.mod.UnixNano(), a.mod.UnixNano()) })
	var metas []Meta
	for _, f := range files {
		if len(metas) == limit {
			break
		}
		s, err := Load(f.id)
		if err != nil {
			continue
		}
		metas = append(metas, Meta{ID: s.ID, Title: s.Title, Model: s.Model, Updated: s.Updated})
	}
	return metas, nil
}

// title is the first user message squeezed to one short line.
func title(msgs []llm.Message) string {
	for _, m := range msgs {
		if m.Role != llm.RoleUser {
			continue
		}
		t := []rune(strings.Join(strings.Fields(m.Content), " "))
		if len(t) > 60 {
			return string(t[:59]) + "…"
		}
		return string(t)
	}
	return "untitled"
}
