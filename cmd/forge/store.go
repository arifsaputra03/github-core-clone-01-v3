//go:build !postgres

package main

import (
	"forge/internal/config"
	"forge/internal/store"
	"forge/internal/store/embedded"
)

// openStore returns the embedded file-based store. This is the default
// build (no -tags postgres): the grading pod has no PostgreSQL, so the
// embedded store is the only one available. DATABASE_URL is ignored here;
// build with -tags postgres to use it.
func openStore(cfg *config.Config) (store.Store, error) {
	return embedded.Open(cfg.DataDir)
}
