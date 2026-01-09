package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}

	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	// Simple, safe defaults for a bot
	d.SetMaxOpenConns(1)
	d.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.PingContext(ctx); err != nil {
		_ = d.Close()
		return nil, err
	}

	return &DB{DB: d}, nil
}
