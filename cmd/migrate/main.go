// Command migrate applies versioned PostgreSQL schema migrations with Goose.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const defaultDatabaseURL = "postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable"

func main() {
	command := "up"
	if len(os.Args) > 1 {
		command = strings.ToLower(strings.TrimSpace(os.Args[1]))
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		log.Fatal(fmt.Errorf("open database: %w", err))
	}
	defer func() { _ = database.Close() }()
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatal(fmt.Errorf("configure goose dialect: %w", err))
	}
	ctx := context.Background()
	switch command {
	case "up":
		err = goose.UpContext(ctx, database, "migrations")
	case "down":
		err = goose.DownContext(ctx, database, "migrations")
	case "status":
		err = goose.StatusContext(ctx, database, "migrations")
	default:
		log.Fatalf("unsupported migration command %q (use up, down, or status)", command)
	}
	if err != nil {
		log.Fatal(fmt.Errorf("goose %s: %w", command, err))
	}
}
