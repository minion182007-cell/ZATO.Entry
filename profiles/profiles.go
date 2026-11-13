// Package profiles содержит встроенные профили территорий.
package profiles

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/minion182007-cell/ZATO.Entry/internal/profile"
)

//go:embed *.json
var files embed.FS

// Registry — набор загруженных профилей.
type Registry struct {
	byID map[string]*profile.Profile
}

// Load читает и проверяет все встроенные профили.
func Load() (*Registry, error) {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil, err
	}
	r := &Registry{byID: map[string]*profile.Profile{}}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := files.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		p, err := profile.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		r.byID[p.ID] = p
	}
	return r, nil
}

// Get возвращает профиль города.
func (r *Registry) Get(id string) (*profile.Profile, bool) {
	p, ok := r.byID[id]
	return p, ok
}

// List возвращает профили, отсортированные по идентификатору.
func (r *Registry) List() []*profile.Profile {
	out := make([]*profile.Profile, 0, len(r.byID))
	for _, p := range r.byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
