package access

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type snapshot struct {
	Roles    map[string]Role    `json:"roles"`
	Scopes   map[string]Scope   `json:"scopes"`
	Bindings map[string]Binding `json:"bindings"`
}

func NewPersistent(path string) (*Engine, error) {
	engine := NewEngine()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return engine, nil
	}
	if err != nil {
		return nil, err
	}
	var state snapshot
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	engine.mu.Lock()
	for id, role := range state.Roles {
		if current, ok := engine.roles[id]; ok && current.System {
			role.System = true
		}
		engine.roles[id] = role
	}
	for id, scope := range state.Scopes {
		if current, ok := engine.scopes[id]; ok && current.System {
			scope.System = true
		}
		engine.scopes[id] = scope
	}
	for id, binding := range state.Bindings {
		engine.bindings[id] = binding
	}
	engine.mu.Unlock()
	return engine, nil
}

func (e *Engine) Save(path string) error {
	data, err := e.MarshalState()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (e *Engine) MarshalState() ([]byte, error) {
	e.mu.RLock()
	state := snapshot{Roles: e.roles, Scopes: e.scopes, Bindings: e.bindings}
	data, err := json.Marshal(state)
	e.mu.RUnlock()
	return data, err
}

func RestoreState(data []byte) (*Engine, error) {
	engine := NewEngine()
	var state snapshot
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	for id, role := range state.Roles {
		if current, ok := engine.roles[id]; ok && current.System {
			role.System = true
		}
		engine.roles[id] = role
	}
	for id, scope := range state.Scopes {
		if current, ok := engine.scopes[id]; ok && current.System {
			scope.System = true
		}
		engine.scopes[id] = scope
	}
	for id, binding := range state.Bindings {
		engine.bindings[id] = binding
	}
	return engine, nil
}
