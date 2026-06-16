package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/example/users-go/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sqlFindByID     = `SELECT id, email, full_name, created_at FROM users WHERE id = $1`
	sqlFindByEmail  = `SELECT id, email, full_name, created_at FROM users WHERE email = $1`
	sqlInsert       = `INSERT INTO users (email, full_name) VALUES ($1, $2) RETURNING id, email, full_name, created_at`
	sqlStreamAll    = `SELECT id, email, full_name, created_at FROM users ORDER BY id`
	sqlStreamPrefix = `SELECT id, email, full_name, created_at FROM users WHERE email LIKE $1 ORDER BY id`
)

// Repository is coroutine-first-style data access: every method returns a
// domain type and never leaks pgx types to the caller. Stateless apart from
// the pool reference; connStr is only needed for the LISTEN/NOTIFY hook, which
// owns a dedicated (non-pooled) connection.
type Repository struct {
	pool    *pgxpool.Pool
	connStr string
}

func NewRepository(pool *pgxpool.Pool, connStr string) *Repository {
	return &Repository{pool: pool, connStr: connStr}
}

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.CreatedAt)
	return u, err
}

// ---- single-row reads ----------------------------------------------------

func (r *Repository) FindByID(ctx context.Context, id int64) (*domain.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, sqlFindByID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *Repository) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, sqlFindByEmail, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ---- write ---------------------------------------------------------------

func (r *Repository) Create(ctx context.Context, in domain.NewUser) (domain.User, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, sqlInsert, in.Email, in.FullName))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return domain.User{}, &domain.DuplicateEmailError{Email: in.Email}
		}
		return domain.User{}, err
	}
	return u, nil
}

// ---- streaming reads -----------------------------------------------------

// StreamAll holds a dedicated connection + transaction open for the whole
// stream and reads a server-side cursor in fetchSize batches, invoking yield
// for each user. Because yield drives the downstream write (gRPC Send / HTTP
// flush), a slow consumer naturally back-pressures the cursor: the next FETCH
// only runs once the previous batch has been handed off. Returning an error
// from yield (e.g. client gone) stops the stream and rolls back cleanly.
func (r *Repository) StreamAll(ctx context.Context, emailPrefix *string, fetchSize int, yield func(domain.User) error) (err error) {
	if fetchSize <= 0 {
		fetchSize = 100
	}
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	// Roll back on any early return; the explicit Commit below makes this a
	// no-op on the happy path.
	defer func() { _ = tx.Rollback(ctx) }()

	declare := "DECLARE users_cur CURSOR FOR " + sqlStreamAll
	var args []any
	if emailPrefix != nil && *emailPrefix != "" {
		declare = "DECLARE users_cur CURSOR FOR " + sqlStreamPrefix
		args = []any{*emailPrefix + "%"}
	}
	if _, err := tx.Exec(ctx, declare, args...); err != nil {
		return err
	}

	fetch := fmt.Sprintf("FETCH FORWARD %d FROM users_cur", fetchSize)
	for {
		rows, err := tx.Query(ctx, fetch)
		if err != nil {
			return err
		}
		batch := make([]domain.User, 0, fetchSize)
		for rows.Next() {
			var u domain.User
			if err := rows.Scan(&u.ID, &u.Email, &u.FullName, &u.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, u)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range batch {
			if err := yield(batch[i]); err != nil {
				return err
			}
		}
		if len(batch) < fetchSize {
			break // cursor exhausted
		}
	}
	return tx.Commit(ctx)
}

// ---- batch ---------------------------------------------------------------

// CreateMany inserts many users in one pipelined round-trip. pgx SendBatch
// streams N parse-bind-execute frames over a single connection, the analogue
// of vertx-pg-client's executeBatch.
func (r *Repository) CreateMany(ctx context.Context, inputs []domain.NewUser) ([]domain.User, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	batch := &pgx.Batch{}
	for _, in := range inputs {
		batch.Queue(sqlInsert, in.Email, in.FullName)
	}
	br := r.pool.SendBatch(ctx, batch)
	defer br.Close()

	out := make([]domain.User, 0, len(inputs))
	for range inputs {
		u, err := scanUser(br.QueryRow())
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// ---- LISTEN/NOTIFY hook --------------------------------------------------

// ListenForNewUsers subscribes to Postgres NOTIFY events on `users_created`
// over a dedicated connection (LISTEN cannot share a pooled connection). Each
// id pushed to the channel was just inserted by any client of the database.
// The goroutine and connection are torn down when ctx is cancelled.
func (r *Repository) ListenForNewUsers(ctx context.Context) (<-chan int64, error) {
	conn, err := pgx.Connect(ctx, r.connStr)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN users_created"); err != nil {
		_ = conn.Close(ctx)
		return nil, err
	}

	out := make(chan int64, 64)
	go func() {
		defer close(out)
		defer conn.Close(context.Background())
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				return // ctx cancelled or connection lost
			}
			id, perr := strconv.ParseInt(n.Payload, 10, 64)
			if perr != nil {
				continue
			}
			select {
			case out <- id:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
