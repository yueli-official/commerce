package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"

	"github.com/gogf/gf/v2/frame/g"
	_ "github.com/lib/pq"
)

func OpenDefaultPostgres(ctx context.Context) (*sql.DB, error) {
	host := g.Cfg().MustGet(ctx, "database.default.host").String()
	port := g.Cfg().MustGet(ctx, "database.default.port", "5432").String()
	name := g.Cfg().MustGet(ctx, "database.default.name").String()
	user := g.Cfg().MustGet(ctx, "database.default.user").String()
	password := g.Cfg().MustGet(ctx, "database.default.pass").String()
	if host == "" || name == "" || user == "" {
		return nil, fmt.Errorf("database.default host, name, and user are required")
	}
	dsn := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   name,
	}
	query := dsn.Query()
	query.Set("sslmode", g.Cfg().MustGet(ctx, "database.default.sslmode", "disable").String())
	dsn.RawQuery = query.Encode()
	database, err := sql.Open("postgres", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("open default PostgreSQL database: %w", err)
	}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping default PostgreSQL database: %w", err)
	}
	return database, nil
}
