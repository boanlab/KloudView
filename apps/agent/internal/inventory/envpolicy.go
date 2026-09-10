package inventory

import "sync"

// envPolicySettings decides whether process environments are collected at all
// and which extra keys keep their value on this node. Collection is off until
// the server enables it, because an environment is where credentials live.
type envPolicySettings struct {
	mu      sync.RWMutex
	enabled bool
	allowed map[string]bool
}

var envPolicy = &envPolicySettings{}

func (p *envPolicySettings) snapshot() (bool, map[string]bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.enabled, p.allowed
}

// SetEnvironmentPolicy applies the node's configuration, as delivered by the
// server on each heartbeat.
func SetEnvironmentPolicy(enabled bool, allowedKeys []string) {
	allowed := make(map[string]bool, len(allowedKeys))
	for _, key := range allowedKeys {
		if key != "" {
			allowed[key] = true
		}
	}
	envPolicy.mu.Lock()
	defer envPolicy.mu.Unlock()
	envPolicy.enabled = enabled
	envPolicy.allowed = allowed
}
