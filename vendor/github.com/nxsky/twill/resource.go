// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package twill

import (
	"fmt"
	"reflect"

	"github.com/nxsky/twill/internal/reflection"
	"github.com/nxsky/twill/runtime/resource"
)

// Database is a field that can be placed inside a component implementation
// struct. Twill automatically fills it with a handle to a SQL database resource
// resolved from local config and environment variables.
type Database struct {
	value resource.Database
}

// Get returns the database handle.
func (d Database) Get() resource.Database {
	return d.value
}

func (d *Database) setResource(v any) error {
	if v == nil {
		return fmt.Errorf("database resource is nil")
	}
	d.value = v.(resource.Database)
	return nil
}

// Cache is a field that can be placed inside a component implementation struct.
// Twill automatically fills it with a handle to a cache resource resolved from
// local config and environment variables.
type Cache struct {
	value resource.Cache
}

// Get returns the cache handle.
func (c Cache) Get() resource.Cache {
	return c.value
}

func (c *Cache) setResource(v any) error {
	if v == nil {
		return fmt.Errorf("cache resource is nil")
	}
	c.value = v.(resource.Cache)
	return nil
}

// PubSub is a field that can be placed inside a component implementation
// struct. Twill automatically fills it with a handle to a pub/sub resource
// resolved from local config and environment variables.
type PubSub struct {
	value resource.PubSub
}

// Get returns the pub/sub handle.
func (p PubSub) Get() resource.PubSub {
	return p.value
}

func (p *PubSub) setResource(v any) error {
	if v == nil {
		return fmt.Errorf("pubsub resource is nil")
	}
	p.value = v.(resource.PubSub)
	return nil
}

// Cron is a field that can be placed inside a component implementation struct.
// Twill automatically fills it with a handle to a cron resource resolved from
// local config and environment variables.
type Cron struct {
	value resource.Cron
}

// Get returns the cron handle.
func (c Cron) Get() resource.Cron {
	return c.value
}

func (c *Cron) setResource(v any) error {
	if v == nil {
		return fmt.Errorf("cron resource is nil")
	}
	c.value = v.(resource.Cron)
	return nil
}

// Secret is a field that can be placed inside a component implementation
// struct. Twill automatically fills it with a handle to a secret resource
// resolved from environment variables.
type Secret struct {
	value resource.Secret
}

// Get returns the secret handle.
func (s Secret) Get() resource.Secret {
	return s.value
}

func (s *Secret) setResource(v any) error {
	if v == nil {
		return fmt.Errorf("secret resource is nil")
	}
	s.value = v.(resource.Secret)
	return nil
}

// HasResources returns whether the provided component implementation has
// resource fields.
func HasResources(impl any) bool {
	p := reflect.ValueOf(impl)
	if p.Kind() != reflect.Pointer {
		return false
	}
	s := p.Elem()
	if s.Kind() != reflect.Struct {
		return false
	}
	for i, n := 0, s.NumField(); i < n; i++ {
		f := s.Field(i)
		if !f.CanAddr() {
			continue
		}
		p := reflect.NewAt(f.Type(), f.Addr().UnsafePointer()).Interface()
		if _, ok := p.(interface{ setResource(any) error }); ok {
			return true
		}
	}
	return false
}

// FillResources initializes resource fields in a component implementation
// struct. The get callback receives the resource name and kind (e.g.,
// "database" or "cache") and should return the matching handle.
func FillResources(impl any, get func(name, kind string) (any, error)) error {
	p := reflect.ValueOf(impl)
	if p.Kind() != reflect.Pointer {
		return fmt.Errorf("FillResources: %T not a pointer", impl)
	}
	s := p.Elem()
	if s.Kind() != reflect.Struct {
		return fmt.Errorf("FillResources: %T not a struct pointer", impl)
	}
	for i, n := 0, s.NumField(); i < n; i++ {
		f := s.Field(i)
		t := s.Type().Field(i)
		if !f.CanAddr() {
			continue
		}
		p := reflect.NewAt(f.Type(), f.Addr().UnsafePointer()).Interface()
		x, ok := p.(interface{ setResource(any) error })
		if !ok {
			continue
		}

		name := t.Name
		if tag, ok := t.Tag.Lookup("twill"); ok {
			name = tag
		}
		kind := resourceKind(f.Type())
		if kind == "" {
			return fmt.Errorf("FillResources: field %v.%s has unsupported resource type %v", s.Type(), t.Name, f.Type())
		}
		v, err := get(name, kind)
		if err != nil {
			return fmt.Errorf("FillResources: setting field %v.%s: %w", s.Type(), t.Name, err)
		}
		if err := x.setResource(v); err != nil {
			return fmt.Errorf("FillResources: setting field %v.%s: %w", s.Type(), t.Name, err)
		}
	}
	return nil
}

func resourceKind(t reflect.Type) string {
	switch t {
	case reflection.Type[Database]():
		return "database"
	case reflection.Type[Cache]():
		return "cache"
	case reflection.Type[PubSub]():
		return "pubsub"
	case reflection.Type[Cron]():
		return "cron"
	case reflection.Type[Secret]():
		return "secret"
	default:
		return ""
	}
}
