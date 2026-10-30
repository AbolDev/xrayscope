// Package panel contains adapters for the VPN panels that XrayScope can sit
// next to. Only 3x-ui is implemented; other panels implement Panel.
package panel

import (
	"context"
	"fmt"

	"xrayscope/internal/model"
)

// Panel reads account state from a VPN panel.
type Panel interface {
	// Name is the panel type, such as "3x-ui".
	Name() string
	// Users returns every account with cumulative traffic counters.
	Users(ctx context.Context) ([]model.PanelUser, error)
	// AccessLog reports where the proxy core writes its access log.
	AccessLog(ctx context.Context) (LogInfo, error)
}

// LogInfo describes the access-log configuration found in the panel.
type LogInfo struct {
	Path     string   `json:"path"`     // resolved existing path, "" if not found
	Setting  string   `json:"setting"`  // raw value from the panel config
	Disabled bool     `json:"disabled"` // access logging is turned off
	Tried    []string `json:"tried"`    // candidate paths that were checked
}

// New creates the adapter for a panel type.
func New(kind, dbPath string) (Panel, error) {
	switch kind {
	case "3x-ui", "3xui", "x-ui":
		return NewXUI(dbPath), nil
	case "none", "":
		return nil, nil
	}
	return nil, fmt.Errorf("unknown panel type %q", kind)
}
