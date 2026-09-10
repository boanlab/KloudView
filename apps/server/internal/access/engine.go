package access

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("not found")

type Permission struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

type Role struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Permissions []Permission `json:"permissions"`
	System      bool         `json:"system"`
}

type Scope struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Paths       []string          `json:"paths,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	System      bool              `json:"system"`
}

type Binding struct {
	ID        string     `json:"id"`
	SubjectID string     `json:"subjectId"`
	RoleID    string     `json:"roleId"`
	ScopeID   string     `json:"scopeId"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

type Decision struct {
	Allowed bool   `json:"allowed"`
	RoleID  string `json:"roleId,omitempty"`
	ScopeID string `json:"scopeId,omitempty"`
	Reason  string `json:"reason"`
}

type Request struct {
	SubjectID    string            `json:"subjectId"`
	Resource     string            `json:"resource"`
	Action       string            `json:"action"`
	ResourcePath string            `json:"resourcePath,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
}

type Engine struct {
	mu       sync.RWMutex
	roles    map[string]Role
	scopes   map[string]Scope
	bindings map[string]Binding
}

func NewEngine() *Engine {
	engine := &Engine{roles: map[string]Role{}, scopes: map[string]Scope{}, bindings: map[string]Binding{}}
	engine.seed()
	return engine
}

func (e *Engine) seed() {
	e.roles["role-admin"] = Role{ID: "role-admin", Name: "Administrator", System: true, Permissions: []Permission{{Resource: "*", Action: "*"}}}
	e.roles["role-viewer"] = Role{ID: "role-viewer", Name: "Viewer", System: true, Permissions: []Permission{{Resource: "*", Action: "read"}}}
	e.roles["role-operator"] = Role{ID: "role-operator", Name: "Operator", System: true, Permissions: []Permission{{Resource: "*", Action: "read"}, {Resource: "alerts", Action: "update"}, {Resource: "incidents", Action: "*"}, {Resource: "operations", Action: "create"}}}
	e.scopes["scope-global"] = Scope{ID: "scope-global", Name: "Global", Paths: []string{"*"}, System: true}
	e.scopes["scope-production"] = Scope{ID: "scope-production", Name: "Production", Paths: []string{"production"}, System: true}
	e.bindings["binding-admin"] = Binding{ID: "binding-admin", SubjectID: "admin", RoleID: "role-admin", ScopeID: "scope-global", CreatedAt: time.Now().UTC()}
}

func (e *Engine) Evaluate(request Request) Decision {
	e.mu.RLock()
	defer e.mu.RUnlock()
	now := time.Now().UTC()
	for _, binding := range e.bindings {
		if binding.SubjectID != request.SubjectID || binding.ExpiresAt != nil && binding.ExpiresAt.Before(now) {
			continue
		}
		role, roleOK := e.roles[binding.RoleID]
		scope, scopeOK := e.scopes[binding.ScopeID]
		if !roleOK || !scopeOK || !scopeMatches(scope, request.ResourcePath, request.Tags) {
			continue
		}
		for _, permission := range role.Permissions {
			if matches(permission.Resource, request.Resource) && matches(permission.Action, request.Action) {
				return Decision{Allowed: true, RoleID: role.ID, ScopeID: scope.ID, Reason: "role_binding"}
			}
		}
	}
	return Decision{Allowed: false, Reason: "no_matching_binding"}
}

func scopeMatches(scope Scope, path string, tags map[string]string) bool {
	pathMatch := len(scope.Paths) == 0
	for _, prefix := range scope.Paths {
		if prefix == "*" || path == prefix || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
			pathMatch = true
			break
		}
	}
	if !pathMatch {
		return false
	}
	for key, value := range scope.Tags {
		if tags[key] != value {
			return false
		}
	}
	return true
}

func matches(pattern, value string) bool { return pattern == "*" || pattern == value }

func (e *Engine) PutRole(role Role) Role {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.roles[role.ID] = role
	return role
}
func (e *Engine) PutScope(scope Scope) Scope {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.scopes[scope.ID] = scope
	return scope
}
func (e *Engine) PutBinding(binding Binding) Binding {
	e.mu.Lock()
	defer e.mu.Unlock()
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = time.Now().UTC()
	}
	e.bindings[binding.ID] = binding
	return binding
}

func (e *Engine) UpdateRole(role Role) (Role, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current, ok := e.roles[role.ID]
	if !ok {
		return Role{}, ErrNotFound
	}
	if current.System {
		return Role{}, errors.New("system role")
	}
	role.System = false
	e.roles[role.ID] = role
	return role, nil
}

func (e *Engine) UpdateScope(scope Scope) (Scope, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current, ok := e.scopes[scope.ID]
	if !ok {
		return Scope{}, ErrNotFound
	}
	if current.System {
		return Scope{}, errors.New("system scope")
	}
	e.scopes[scope.ID] = scope
	return scope, nil
}

func (e *Engine) CreateBinding(binding Binding) (Binding, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.roles[binding.RoleID]; !ok {
		return Binding{}, errors.New("role not found")
	}
	if _, ok := e.scopes[binding.ScopeID]; !ok {
		return Binding{}, errors.New("scope not found")
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = time.Now().UTC()
	}
	e.bindings[binding.ID] = binding
	return binding, nil
}

func (e *Engine) UpdateBinding(binding Binding) (Binding, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current, ok := e.bindings[binding.ID]
	if !ok {
		return Binding{}, ErrNotFound
	}
	if _, ok := e.roles[binding.RoleID]; !ok {
		return Binding{}, errors.New("role not found")
	}
	if _, ok := e.scopes[binding.ScopeID]; !ok {
		return Binding{}, errors.New("scope not found")
	}
	binding.CreatedAt = current.CreatedAt
	e.bindings[binding.ID] = binding
	return binding, nil
}

func (e *Engine) Roles() []Role {
	e.mu.RLock()
	defer e.mu.RUnlock()
	items := make([]Role, 0, len(e.roles))
	for _, item := range e.roles {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
func (e *Engine) Scopes() []Scope {
	e.mu.RLock()
	defer e.mu.RUnlock()
	items := make([]Scope, 0, len(e.scopes))
	for _, item := range e.scopes {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
func (e *Engine) Bindings() []Binding {
	e.mu.RLock()
	defer e.mu.RUnlock()
	items := make([]Binding, 0, len(e.bindings))
	for _, item := range e.bindings {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (e *Engine) Scope(id string) (Scope, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	scope, ok := e.scopes[id]
	return scope, ok
}

func (e *Engine) Binding(id string) (Binding, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	binding, ok := e.bindings[id]
	return binding, ok
}

func (e *Engine) DeleteRole(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if role, ok := e.roles[id]; !ok {
		return ErrNotFound
	} else if role.System {
		return errors.New("system role")
	}
	for _, binding := range e.bindings {
		if binding.RoleID == id {
			return errors.New("role has bindings")
		}
	}
	delete(e.roles, id)
	return nil
}
func (e *Engine) DeleteScope(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	scope, ok := e.scopes[id]
	if !ok {
		return ErrNotFound
	}
	if scope.System {
		return errors.New("system scope")
	}
	for _, binding := range e.bindings {
		if binding.ScopeID == id {
			return errors.New("scope has bindings")
		}
	}
	delete(e.scopes, id)
	return nil
}
func (e *Engine) DeleteBinding(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.bindings[id]; !ok {
		return ErrNotFound
	}
	delete(e.bindings, id)
	return nil
}
