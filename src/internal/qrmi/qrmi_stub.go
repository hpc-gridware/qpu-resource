// Copyright 2026 Pasqal, HPC Gridware GmbH and its contributors
// SPDX-License-Identifier: Apache-2.0

//go:build !qrmi

package qrmi

// Config is the stub equivalent of the cgo-backed type. All methods return
// ErrNotAvailable so callers can fail fast when the package is built
// without the qrmi tag.
type Config struct{}

// LoadConfig always returns ErrNotAvailable in stub builds.
func LoadConfig(path string) (*Config, error) { return nil, ErrNotAvailable }

// Close is a no-op in stub builds.
func (c *Config) Close() {}

// ResourceDef always returns ErrNotAvailable in stub builds.
func (c *Config) ResourceDef(name string) (*ResourceDef, error) { return nil, ErrNotAvailable }

// ResourceDef is the stub equivalent of the cgo-backed type.
type ResourceDef struct{}

// Close is a no-op in stub builds.
func (d *ResourceDef) Close() {}

// Type returns 0 in stub builds.
func (d *ResourceDef) Type() int { return 0 }

// TypeString returns the empty string in stub builds.
func (d *ResourceDef) TypeString() string { return "" }

// Environments returns nil in stub builds.
func (d *ResourceDef) Environments() []EnvVar { return nil }

// Resource is the stub equivalent of the cgo-backed type.
type Resource struct{}

// NewResource always returns ErrNotAvailable in stub builds.
func NewResource(name string, typ int) (*Resource, error) { return nil, ErrNotAvailable }

// Close is a no-op in stub builds.
func (r *Resource) Close() {}

// IsAccessible always returns ErrNotAvailable in stub builds.
func (r *Resource) IsAccessible() (bool, error) { return false, ErrNotAvailable }

// Acquire always returns ErrNotAvailable in stub builds.
func (r *Resource) Acquire() (string, error) { return "", ErrNotAvailable }

// Release always returns ErrNotAvailable in stub builds.
func (r *Resource) Release(token string) error { return ErrNotAvailable }

// SetLogCallback is a no-op in stub builds.
func SetLogCallback(fn LogFunc) {}
