//go:build integration

// Package integration holds tests that need a real PostgreSQL, not a mock
// — row locking and transaction behavior cannot be verified against a
// fake. They are gated behind the "integration" build tag (run via `make
// test-integration`, not plain `go test ./...`) because each one spins up
// a real container via testcontainers-go, which `go test ./...` should
// never do implicitly.
package integration

import (
	"context"
	"database/sql"
	"food-delivery-mvp/migrations"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// newTestPool starts a throwaway Postgres container, brings it to the
// current schema with the project's own goose migrations (so this test
// exercises the exact same schema production runs, not a hand-copied
// approximation of it), and returns a pool connected to it. Both the pool
// and the container are torn down via t.Cleanup.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("food_delivery_test"),
		postgres.WithUsername("food_delivery"),
		postgres.WithPassword("food_delivery"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	migrate(t, dsn)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return pool
}

// migrate applies every migration in migrations.FS via goose — the same
// embedded filesystem cmd/migrate uses, so this test can never drift from
// what production actually runs.
func migrate(t *testing.T, dsn string) {
	t.Helper()

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db for migrations: %v", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "."); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
}
