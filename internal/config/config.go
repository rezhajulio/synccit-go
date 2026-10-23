// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

package config

import "os"

// Config holds runtime configuration for the synccit server and CLI.
type Config struct {
	DBPath            string
	Addr              string
	AllowRegistration bool
}

// FromEnv builds a Config from SYNCCIT_* environment variables, falling back
// to the defaults from the project brief: DB "./synccit.db", address ":8080",
// registration disabled.
func FromEnv() Config {
	cfg := Config{
		DBPath: "./synccit.db",
		Addr:   ":8080",
	}
	if v := os.Getenv("SYNCCIT_DB"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("SYNCCIT_ADDR"); v != "" {
		cfg.Addr = v
	}
	switch os.Getenv("SYNCCIT_ALLOW_REGISTRATION") {
	case "true", "1":
		cfg.AllowRegistration = true
	}
	return cfg
}
