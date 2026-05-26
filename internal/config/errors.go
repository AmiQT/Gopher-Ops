package config

import "errors"

// Sentinel errors callers can match with errors.Is. These let downstream
// code distinguish "missing-secret" from "malformed-value" without parsing
// error strings.
var (
	// ErrMissingRequired is returned when a required env var is unset.
	ErrMissingRequired = errors.New("required config value missing")
	// ErrOutOfRange is returned when a numeric env var is outside its allowed range.
	ErrOutOfRange = errors.New("config value out of allowed range")
	// ErrMalformed is returned when an env var cannot be parsed as its expected type.
	ErrMalformed = errors.New("config value malformed")
)
