package main

import (
	"xrayscope/internal/config"
	"xrayscope/internal/panel"
)

func newPanel(cfg config.Config) (panel.Panel, error) {
	return panel.New(cfg.Panel.Type, cfg.Panel.DBPath)
}
