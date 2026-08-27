package mockstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/SemperSupra/garm-provider-truenas/internal/provider"
)

type Store struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Store {
	return &Store{path: path}
}

func (s *Store) CreateApp(_ context.Context, spec provider.AppSpec) (provider.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.load()
	if err != nil {
		return provider.App{}, err
	}
	if app, ok := apps[spec.Name]; ok {
		return app, nil
	}
	app := provider.App{Spec: spec, State: provider.StateRunning}
	apps[spec.Name] = app
	if err := s.save(apps); err != nil {
		return provider.App{}, err
	}
	return app, nil
}

func (s *Store) GetApp(_ context.Context, name string) (provider.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.load()
	if err != nil {
		return provider.App{}, err
	}
	app, ok := apps[name]
	if !ok {
		return provider.App{}, provider.ErrNotFound
	}
	return app, nil
}

func (s *Store) ListApps(_ context.Context) ([]provider.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]provider.App, 0, len(apps))
	for _, app := range apps {
		out = append(out, app)
	}
	return out, nil
}

func (s *Store) DeleteApp(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := apps[name]; !ok {
		return nil
	}
	delete(apps, name)
	return s.save(apps)
}

func (s *Store) SetState(name string, state provider.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	apps, err := s.load()
	if err != nil {
		return err
	}
	app, ok := apps[name]
	if !ok {
		return provider.ErrNotFound
	}
	app.State = state
	apps[name] = app
	return s.save(apps)
}

func (s *Store) load() (map[string]provider.App, error) {
	apps := map[string]provider.App{}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return apps, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return apps, nil
	}
	if err := json.Unmarshal(data, &apps); err != nil {
		return nil, err
	}
	return apps, nil
}

func (s *Store) save(apps map[string]provider.App) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(apps, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
