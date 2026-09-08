package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, args json.RawMessage) error
}

type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds a tool; a duplicate name panics (wiring is static).
func (r *Registry) Register(t Tool) {
	if _, ok := r.tools[t.Name()]; ok {
		panic(fmt.Sprintf("tool: duplicate registration: %s", t.Name()))
	}
	r.tools[t.Name()] = t
}

// Get returns the tool registered under name.
func (r *Registry) Get(name string) (Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool: not registered: %s", name)
	}
	return t, nil
}

// Names returns the registered tool names, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
