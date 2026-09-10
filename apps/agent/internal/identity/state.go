package identity

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type State struct {
	AgentID    string `json:"agentId"`
	NodeID     string `json:"nodeId"`
	Credential string `json:"credential"`
}

func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.AgentID == "" || state.NodeID == "" || state.Credential == "" {
		return State{}, errors.New("incomplete agent identity")
	}
	return state, nil
}

func Save(path string, state State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
