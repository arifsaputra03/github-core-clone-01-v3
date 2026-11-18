//go:build postgres

package main

import (
	"forge/internal/config"
	"forge/internal/store"
	"forge/internal/store/embedded"
	"forge/internal/store/postgres"
)

// openStore returns the PostgreSQL store when DATABASE_URL is set, and the
// embedded file-based store otherwise. Only available in a build with
// -tags postgres.
func openStore(cfg *config.Config) (store.Store, error) {
	if cfg.DatabaseURL != "" {
		return postgres.Open(cfg.DatabaseURL)
	}
	return embedded.Open(cfg.DataDir)
}
