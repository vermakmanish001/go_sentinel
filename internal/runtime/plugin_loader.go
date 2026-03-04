package runtime

import (
	"fmt"
	"plugin"
	"sync"

	"go.uber.org/zap"
)

// Plugin defines the interface for load shape plugins
type Plugin interface {
	// Name returns the plugin name
	Name() string
	
	// Execute executes the plugin logic and returns the number of virtual users
	// at the given timestamp (seconds since test start)
	Execute(timestampSeconds int64, params map[string]interface{}) (int32, error)
}

// PluginLoader loads and manages plugins
type PluginLoader struct {
	logger  *zap.Logger
	plugins map[string]Plugin
	mu      sync.RWMutex
}

// NewPluginLoader creates a new plugin loader
func NewPluginLoader(logger *zap.Logger) *PluginLoader {
	return &PluginLoader{
		logger:  logger,
		plugins: make(map[string]Plugin),
	}
}

// LoadPlugin loads a plugin from a .so file
func (pl *PluginLoader) LoadPlugin(path string) error {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	p, err := plugin.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open plugin: %w", err)
	}

	symbol, err := p.Lookup("Plugin")
	if err != nil {
		return fmt.Errorf("plugin must export 'Plugin' symbol: %w", err)
	}

	pluginInstance, ok := symbol.(Plugin)
	if !ok {
		return fmt.Errorf("plugin does not implement Plugin interface")
	}

	name := pluginInstance.Name()
	pl.plugins[name] = pluginInstance

	pl.logger.Info("loaded plugin", zap.String("name", name), zap.String("path", path))
	return nil
}

// GetPlugin retrieves a plugin by name
func (pl *PluginLoader) GetPlugin(name string) (Plugin, error) {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	p, ok := pl.plugins[name]
	if !ok {
		return nil, fmt.Errorf("plugin not found: %s", name)
	}

	return p, nil
}

// ListPlugins returns a list of loaded plugin names
func (pl *PluginLoader) ListPlugins() []string {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	names := make([]string, 0, len(pl.plugins))
	for name := range pl.plugins {
		names = append(names, name)
	}

	return names
}
