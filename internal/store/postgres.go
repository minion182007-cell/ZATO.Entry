package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	_ "github.com/lib/pq" // драйвер PostgreSQL

	"github.com/minion182007-cell/ZATO.Entry/internal/passes"
	"github.com/minion182007-cell/ZATO.Entry/internal/planner"
)

//go:embed migrations/*.sql
var migrations embed.FS

const dateLayout = "2006-01-02"

// migrationLock — ключ advisory-блокировки: миграции не накатываются
// одновременно несколькими экземплярами сервиса.
const migrationLock = 7204101

// Postgres — хранилище в PostgreSQL.
type Postgres struct {
	db *sql.DB
}

// OpenPostgres подключается к базе и применяет миграции.
func OpenPostgres(ctx context.Context, url string) (*Postgres, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	p := &Postgres{db: db}
	if err := p.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("подключение к базе: %w", err)
	}
	if err := p.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("миграции: %w", err)
	}
	return p, nil
}

func (p *Postgres) migrate(ctx context.Context) error {
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLock); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLock)

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var done bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) SavePlan(ctx context.Context, plan *planner.Plan, orgID string) error {
	id, err := randomHex(12)
	if err != nil {
		return err
	}
	plan.ID = id
	body, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `INSERT INTO plans (id, org_id, city, scenario, title, start_date, target_date, body)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8)`,
		id, orgID, plan.City, plan.Scenario, plan.Title, plan.StartDate, plan.TargetDate, body)
	return err
}

func (p *Postgres) GetPlan(ctx context.Context, id string) (*planner.Plan, error) {
	var body []byte
	err := p.db.QueryRowContext(ctx, `SELECT body FROM plans WHERE id = $1`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var plan planner.Plan
	if err := json.Unmarshal(body, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (p *Postgres) ListPlans(ctx context.Context, orgID string) ([]PlanSummary, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id, scenario, title, start_date, target_date, created_at
		FROM plans WHERE org_id = $1 ORDER BY created_at DESC LIMIT 200`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlanSummary{}
	for rows.Next() {
		var s PlanSummary
		var start, target time.Time
		if err := rows.Scan(&s.ID, &s.Scenario, &s.Title, &start, &target, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.StartDate, s.TargetDate = start.Format(dateLayout), target.Format(dateLayout)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *Postgres) PurgeAnonymousPlans(ctx context.Context, before time.Time) (int64, error) {
	res, err := p.db.ExecContext(ctx, `DELETE FROM plans WHERE org_id IS NULL AND created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (p *Postgres) CreateOrg(ctx context.Context, name, city string) (*Org, string, error) {
	org, token, err := newOrg(name, city)
	if err != nil {
		return nil, "", err
	}
	err = p.db.QueryRowContext(ctx, `INSERT INTO orgs (id, name, city, token_hash, feed_token)
		VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
		org.ID, org.Name, org.City, hashToken(token), org.FeedToken).Scan(&org.CreatedAt)
	if err != nil {
		return nil, "", err
	}
	return org, token, nil
}

func (p *Postgres) OrgByToken(ctx context.Context, token string) (*Org, error) {
	return p.org(ctx, `token_hash`, hashToken(token))
}

func (p *Postgres) OrgByFeedToken(ctx context.Context, feed string) (*Org, error) {
	return p.org(ctx, `feed_token`, feed)
}

// column — только константы из кода, не пользовательский ввод.
func (p *Postgres) org(ctx context.Context, column, value string) (*Org, error) {
	var o Org
	err := p.db.QueryRowContext(ctx, `SELECT id, name, city, feed_token, created_at FROM orgs WHERE `+column+` = $1`, value).
		Scan(&o.ID, &o.Name, &o.City, &o.FeedToken, &o.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (p *Postgres) ListStaff(ctx context.Context, orgID string) ([]passes.Employee, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT code, name, pass_expires FROM staff WHERE org_id = $1 ORDER BY code`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []passes.Employee{}
	for rows.Next() {
		var e passes.Employee
		var exp time.Time
		if err := rows.Scan(&e.ID, &e.Name, &exp); err != nil {
			return nil, err
		}
		e.PassExpires = exp.Format(dateLayout)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Postgres) PutStaff(ctx context.Context, orgID string, staff []passes.Employee) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Блокировка строки организации делает проверку лимита атомарной.
	var exists bool
	err = tx.QueryRowContext(ctx, `SELECT true FROM orgs WHERE id = $1 FOR UPDATE`, orgID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO staff (org_id, code, name, pass_expires)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_id, code) DO UPDATE
		SET name = EXCLUDED.name, pass_expires = EXCLUDED.pass_expires, updated_at = now()`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range staff {
		if _, err := stmt.ExecContext(ctx, orgID, e.ID, e.Name, e.PassExpires); err != nil {
			return err
		}
	}
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM staff WHERE org_id = $1`, orgID).Scan(&total); err != nil {
		return err
	}
	if total > MaxStaff {
		return ErrLimit
	}
	return tx.Commit()
}

func (p *Postgres) GetStaff(ctx context.Context, orgID, code string) (*passes.Employee, error) {
	var e passes.Employee
	var exp time.Time
	err := p.db.QueryRowContext(ctx, `SELECT code, name, pass_expires FROM staff WHERE org_id = $1 AND code = $2`, orgID, code).
		Scan(&e.ID, &e.Name, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.PassExpires = exp.Format(dateLayout)
	return &e, nil
}

func (p *Postgres) DeleteStaff(ctx context.Context, orgID, code string) error {
	res, err := p.db.ExecContext(ctx, `DELETE FROM staff WHERE org_id = $1 AND code = $2`, orgID, code)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) Ping(ctx context.Context) error { return p.db.PingContext(ctx) }
func (p *Postgres) Close() error                   { return p.db.Close() }
