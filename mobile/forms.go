package mobile

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/PeaChack/Kordo/core/model"
	"github.com/PeaChack/Kordo/core/providers"
)

// The app edits servers with per-protocol forms. Params are the provider's
// own JSON (the same fields as its ConfigSchema).

type parsed struct {
	Provider string          `json:"provider"`
	Name     string          `json:"name"`
	Params   json.RawMessage `json:"params"`
}

// ParseServer turns a link or a WireGuard .conf into {provider, name, params}
// without saving it, so the app can fill its form from a paste.
func ParseServer(text string) (string, error) {
	pid, params, name, err := providers.ParseAny(strings.TrimSpace(text))
	if err != nil {
		return "", err
	}
	return toJSON(parsed{pid, name, params}), nil
}

// ServerParams returns {provider, name, params} of a saved server for editing.
func ServerParams(id string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	for _, s := range st.Servers {
		if s.ID == id {
			return toJSON(parsed{s.ProviderID, s.Name, s.Params}), nil
		}
	}
	return "", errors.New("server not found")
}

func checkParams(provider, params string) (json.RawMessage, error) {
	p, err := providers.Get(provider)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(params)
	if !json.Valid(raw) {
		return nil, errors.New("invalid params")
	}
	if err := p.Validate(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// SaveServer adds a server (id == "") or replaces one, from form params.
// Returns the server id.
func SaveServer(id, provider, name, params string) (string, error) {
	raw, err := checkParams(provider, params)
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = provider
	}
	mu.Lock()
	defer mu.Unlock()
	if id == "" {
		srv := model.Server{ID: newID(), ProviderID: provider, Params: raw, Name: name, Enabled: true}
		st.Servers = append(st.Servers, srv)
		return srv.ID, saveLocked()
	}
	for i := range st.Servers {
		if st.Servers[i].ID == id {
			st.Servers[i].ProviderID, st.Servers[i].Params, st.Servers[i].Name = provider, raw, name
			return id, saveLocked()
		}
	}
	return "", errors.New("server not found")
}
