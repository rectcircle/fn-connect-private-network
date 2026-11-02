package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
	data, err := os.ReadFile(s.path + ".links")
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
	f, err := s.readLinks()
	if err != nil {
		return err
	}
	f.NAS[fn] = links
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return writeClientFileAtomic(s.path+".links", append(data, '\n'))
}
func (m *Manager) LinkSettings(ctx context.Context) (LinkSettings, error) {
	cfg, err := m.store.Load()
	if err != nil {
		return LinkSettings{}, err
	}
	if cfg == nil {
		return LinkSettings{Links: defaultLinks()}, nil
	}
	links, err := m.store.loadLinks(cfg.FNID)
	return LinkSettings{cfg.FNID, links}, err
}
func (m *Manager) SetLinkSettings(ctx context.Context, input LinkSettings) (LinkSettings, error) {
	cfg, err := m.store.Load()
	if err == nil && (cfg == nil || cfg.FNID != input.FNID) {
		err = model.NewError(model.ErrorConflict, "NAS changed; reload link settings", false)
	}
	if err == nil {
		err = m.store.saveLinks(cfg.FNID, input.Links)
	}
	if err != nil {
		return LinkSettings{}, err
	}
	m.scheduleConnection(workReconcile)
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
