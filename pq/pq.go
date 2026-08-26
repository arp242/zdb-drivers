// Package pq provides a zdb driver for PostgreSQL.
//
// This uses https://github.com/lib/pq
package pq

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/lib/pq/pqerror"
	"zgo.at/zdb"
	"zgo.at/zdb/drivers"
	"zgo.at/zstd/zcrypto"
	"zgo.at/zstd/zfs"
)

func init() {
	drivers.RegisterDriver(driver{})
}

type driver struct{}

func (driver) Name() string             { return "pq" }
func (driver) Dialect() string          { return "postgresql" }
func (driver) ErrUnique(err error) bool { return pq.As(err, pqerror.UniqueViolation) != nil }
func (d driver) Connect(ctx context.Context, dsn string, create bool) (*sql.DB, error) {
	cfg, err := pq.NewConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("zdb-pq.Connect: %w", err)
	}
	conn, err := pq.NewConnectorConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("zdb-pq.Connect: %w", err)
	}

	db := sql.OpenDB(conn)
	err = db.PingContext(ctx)
	if err != nil && !create {
		if cfg.Database != "" {
			return nil, &drivers.NotExistError{Driver: "postgres", DB: cfg.Database, Connect: dsn}
		}
		return nil, fmt.Errorf("zdb-pq.Connect: %w", err)
	}
	if err != nil {
		dbname := cfg.Database
		cfg.Database = "postgres"
		conn, err := pq.NewConnectorConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("zdb-pq.Connect: %w", err)
		}
		db := sql.OpenDB(conn)
		defer db.Close()
		_, err = db.ExecContext(ctx, fmt.Sprintf(`create database "%s"`, dbname))
		if err != nil {
			return nil, fmt.Errorf("zdb-pq.Connect: %w", err)
		}
		return d.Connect(ctx, dsn, false) // Restart with create=false to avoid being stuck in a loop.
	}
	return db, nil
}

// StartTest starts a new test.
//
// Every test runs in its own schema inside the zdb_test database. All of this
// is automatically created from opt.Files.
//
// The connect string can be customised via opt.Connect.
func (driver) StartTest(t testing.TB, opt *drivers.TestOptions) context.Context {
	t.Helper()

	if e := os.Getenv("PGDATABASE"); e == "" {
		os.Setenv("PGDATABASE", "zdb_test")
	}
	if opt == nil {
		opt = &drivers.TestOptions{}
	}

	schema := fmt.Sprintf(`"zdb_test_%s_%s"`, time.Now().Format("20060102T15:04:05"), zcrypto.SecretString(4, ""))
	cfg, err := pq.NewConfig(opt.Connect)
	if err != nil {
		t.Fatalf("zdb-pq.StartTest: parsing config %q: %s", opt.Connect, err)
	}
	cfg.Runtime["search_path"] = schema

	conn, err := pq.NewConnectorConfig(cfg)
	if err != nil {
		t.Fatalf("zdb-pq.StartTest: creating connector %s: %s", schema, err)
	}

	db, err := zdb.FromSQLDB(sql.OpenDB(conn))
	if err != nil {
		t.Fatal(err)
	}

	err = db.Exec(t.Context(), `create schema `+schema)
	if err != nil {
		t.Fatalf("zdb-pq.StartTest: creating schema %s: %s", schema, err)
	}
	t.Cleanup(func() {
		err := db.Exec(context.Background(), "drop schema "+schema+" cascade")
		if err != nil {
			t.Logf("dropping schema %s: %s", schema, err)
		}
		db.Close()
	})

	if opt.Files != nil {
		dbFiles, err := fs.Sub(opt.Files, "db")
		if err == nil {
			if err := zdb.Create(db, dbFiles); err != nil {
				t.Fatalf("zdb-pq.StartTest: creating tables: %s", err)
			}
		}
		if zfs.Exists(opt.Files, "db/migrate") {
			m, err := zdb.NewMigrate(db, opt.Files, opt.GoMigrations)
			if err != nil {
				t.Fatalf("zdb-pq.StartTest: creating migrator: %s", err)
			}
			if err := m.Run("all"); err != nil {
				t.Fatalf("zdb-pq.StartTest: running migrations: %s", err)
			}
		}
	}

	return zdb.WithDB(t.Context(), db)
}
