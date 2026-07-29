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

package resource

import (
	"database/sql"
	"fmt"
)

// Database is a SQL database handle. Implementations must return a *sql.DB that
// the application owns; the runtime does not manage connection lifetimes.
type Database interface {
	StdDB() *sql.DB
}

type sqlDatabase struct {
	db *sql.DB
}

func (d *sqlDatabase) StdDB() *sql.DB {
	if d == nil {
		return nil
	}
	return d.db
}

// OpenDatabase opens a database/sql database using driver and dsn. The caller
// must import the desired driver package before calling OpenDatabase.
func OpenDatabase(driver, dsn string) (Database, error) {
	if driver == "" {
		return nil, fmt.Errorf("database driver is required")
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &sqlDatabase{db: db}, nil
}
