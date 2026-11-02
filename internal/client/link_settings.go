package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type LinkPreference struct {
	Link    string `json:"link"`
	Enabled bool   `json:"enabled"`
}
type LinkSettings struct {
	FNID  string           `json:"fnId"`
	Links []LinkPreference `json:"links"`
}

func defaultLinks() []LinkPreference { return []LinkPreference{{"ipv6", true}, {"fn-connect", true}} }
func validateLinks(links []LinkPreference) error {
	if len(links) != 2 || links[0].Link == links[1].Link {
		return model.NewError(model.ErrorInvalidArgument, "exactly ipv6 and fn-connect are required; local is always first and enabled", false)
	}
	for _, l := range links {
		if l.Link != "ipv6" && l.Link != "fn-connect" {
			return model.NewError(model.ErrorInvalidArgument, "unknown link; local cannot be changed", false)
		}
	}
	return nil
}

type linkSettingsFile struct {
	Version int                         `json:"version"`
	NAS     map[string][]LinkPreference `json:"nas"`
}

var linkFileMu sync.Mutex

func (s *ConfigStore) readLinks() (linkSettingsFile, error) {
	f := linkSettingsFile{Version: 1, NAS: map[string][]LinkPreference{}}
	data, err := os.ReadFile(s.linksPath())
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if len(data) > maxLocalConfigSize {
		return f, errors.New("link settings exceed size limit")
	}
	if err := model.DecodeStrict(data, &f); err != nil {
		return f, err
	}
	if f.Version != 1 || f.NAS == nil {
		return f, errors.New("unsupported link settings schema")
	}
	for _, links := range f.NAS {
		if err := validateLinks(links); err != nil {
			return f, err
		}
	}
	return f, nil
}
func (s *ConfigStore) loadLinks(fn string) ([]LinkPreference, error) {
	linkFileMu.Lock()
	defer linkFileMu.Unlock()
	f, err := s.readLinks()
	if err != nil {
		return nil, err
	}
	if links, ok := f.NAS[fn]; ok {
		return links, nil
	}
	return defaultLinks(), nil
}
func (s *ConfigStore) saveLinks(fn string, links []LinkPreference) error {
	if err := validateLinks(links); err != nil {
		return err
	}
	linkFileMu.Lock()
	defer linkFileMu.Unlock()
	unlock, lockErr := s.lockSharedLinks()
	if lockErr != nil {
		return lockErr
	}
	defer unlock()
	f, err := s.readLinks()
	if err != nil {
		return err
	}
	f.NAS[fn] = links
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return writeClientFileAtomic(s.linksPath(), append(data, '\n'))
}
func (m *Manager) LinkSettings(ctx context.Context) (LinkSettings, error) {
	return m.store.LinkSettings(ctx)
}
func (s *ConfigStore) LinkSettings(ctx context.Context) (LinkSettings, error) {
	if s.path == "" && s.sharedLinks != "" {
		data, err := os.ReadFile(filepath.Join(s.sharedLinks, "current-nas.json"))
		if os.IsNotExist(err) {
			return LinkSettings{Links: defaultLinks()}, nil
		}
		if err != nil {
			return LinkSettings{}, err
		}
		var current linkNASReference
		if err := model.DecodeStrict(data, &current); err != nil {
			return LinkSettings{}, err
		}
		if current.Version != 1 {
			return LinkSettings{}, errors.New("unsupported current NAS settings schema")
		}
		links, err := s.loadLinks(current.FNID)
		return LinkSettings{FNID: current.FNID, Links: links}, err
	}
	cfg, err := s.Load()
	if err != nil {
		return LinkSettings{}, err
	}
	if cfg == nil {
		return LinkSettings{Links: defaultLinks()}, nil
	}
	links, err := s.loadLinks(cfg.FNID)
	return LinkSettings{cfg.FNID, links}, err
}
func (m *Manager) SetLinkSettings(ctx context.Context, input LinkSettings) (LinkSettings, error) {
	result, err := m.store.SetLinkSettings(ctx, input)
	if err == nil {
		m.scheduleConnection(workReconcile)
	}
	return result, err
}
func (s *ConfigStore) SetLinkSettings(ctx context.Context, input LinkSettings) (LinkSettings, error) {
	if err := ctx.Err(); err != nil {
		return LinkSettings{}, err
	}
	current, err := s.LinkSettings(ctx)
	if err != nil {
		return LinkSettings{}, err
	}
	if current.FNID == "" || current.FNID != input.FNID {
		return LinkSettings{}, model.NewError(model.ErrorConflict, "NAS changed; reload link settings", false)
	}
	if err := s.saveLinks(input.FNID, input.Links); err != nil {
		return LinkSettings{}, err
	}
	return input, nil
}

type forcedLinkKey struct{}

func linkEnabled(links []LinkPreference, name string) bool {
	for _, l := range links {
		if l.Link == name {
			return l.Enabled
		}
	}
	return false
}

func (s *ConfigStore) linksPath() string {
	if s.sharedLinks != "" {
		return filepath.Join(s.sharedLinks, "links.json")
	}
	return s.path + ".links"
}

// Shared preferences contain only NAS IDs and link policy, never identity or credentials.
func SharedLinkSettingsStore(directory string) *ConfigStore {
	return &ConfigStore{sharedLinks: directory}
}
func (s *ConfigStore) UseSharedLinkSettings(directory string) error {
	if !filepath.IsAbs(directory) {
		return errors.New("absolute shared settings directory required")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	s.sharedLinks = directory
	unlock, err := s.lockSharedLinks()
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.validateLinkNASReference(); err != nil {
		return err
	}
	if _, err := os.Stat(s.linksPath()); os.IsNotExist(err) {
		data, readErr := os.ReadFile(s.path + ".links")
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if readErr == nil {
			var old linkSettingsFile
			if err := model.DecodeStrict(data, &old); err != nil {
				return err
			}
			if old.Version != 1 || old.NAS == nil {
				return errors.New("unsupported link settings schema")
			}
			for _, links := range old.NAS {
				if err := validateLinks(links); err != nil {
					return err
				}
			}
			if err := writeClientFileAtomic(s.linksPath(), data); err != nil {
				return err
			}
		}
	} else if err != nil {
		return err
	}
	if _, err := s.readLinks(); err != nil {
		return err
	}
	cfg, err := s.Load()
	if err != nil {
		return err
	}
	if cfg != nil {
		return s.publishLinkNAS(cfg.FNID)
	}
	return s.publishLinkNAS("")
}

type linkNASReference struct {
	Version int    `json:"version"`
	FNID    string `json:"fnId"`
}

func (s *ConfigStore) validateLinkNASReference() error {
	path := filepath.Join(s.sharedLinks, "current-nas.json")
	old, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var previous linkNASReference
	if err := model.DecodeStrict(old, &previous); err != nil {
		return err
	}
	if previous.Version != 1 {
		return errors.New("unsupported current NAS settings schema")
	}
	return nil
}
func (s *ConfigStore) publishLinkNAS(fn string) error {
	if s.sharedLinks == "" {
		return nil
	}
	if err := s.validateLinkNASReference(); err != nil {
		return err
	}
	data, err := json.Marshal(linkNASReference{Version: 1, FNID: fn})
	if err != nil {
		return err
	}
	return writeClientFileAtomic(filepath.Join(s.sharedLinks, "current-nas.json"), append(data, '\n'))
}
