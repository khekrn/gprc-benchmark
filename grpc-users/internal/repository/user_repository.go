package repository

import (
	"context"
	"fmt"
	"time"

	pb "coding2fun.in/grpc-users/pkg/user/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type userRepository struct {
	db *pgxpool.Pool
}

func NewUserRepository(db *pgxpool.Pool) UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) Create(ctx context.Context, req *pb.CreateUserRequest) (*pb.User, error) {
	query := `
        INSERT INTO users (email, first_name, last_name, phone_number, is_active, created_at, updated_at)
        VALUES ($1, $2, $3, $4, true, NOW(), NOW())
        RETURNING id, email, first_name, last_name, phone_number, is_active, created_at, updated_at
    `

	var user pb.User
	var createdAt, updatedAt time.Time

	err := r.db.QueryRow(ctx, query, req.Email, req.FirstName, req.LastName, req.PhoneNumber).Scan(
		&user.Id,
		&user.Email,
		&user.FirstName,
		&user.LastName,
		&user.PhoneNumber,
		&user.IsActive,
		&createdAt,
		&updatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	user.CreatedAt = timestamppb.New(createdAt)
	user.UpdatedAt = timestamppb.New(updatedAt)

	return &user, nil
}

func (r *userRepository) GetByID(ctx context.Context, id int64) (*pb.User, error) {
	query := `
        SELECT id, email, first_name, last_name, phone_number, is_active, created_at, updated_at
        FROM users
        WHERE id = $1
    `

	var user pb.User
	var createdAt, updatedAt time.Time

	err := r.db.QueryRow(ctx, query, id).Scan(
		&user.Id,
		&user.Email,
		&user.FirstName,
		&user.LastName,
		&user.PhoneNumber,
		&user.IsActive,
		&createdAt,
		&updatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	user.CreatedAt = timestamppb.New(createdAt)
	user.UpdatedAt = timestamppb.New(updatedAt)

	return &user, nil
}

func (r *userRepository) Update(ctx context.Context, req *pb.UpdateUserRequest) (*pb.User, error) {
	query := `
        UPDATE users 
        SET email = $2, first_name = $3, last_name = $4, phone_number = $5, is_active = $6, updated_at = NOW()
        WHERE id = $1
        RETURNING id, email, first_name, last_name, phone_number, is_active, created_at, updated_at
    `

	var user pb.User
	var createdAt, updatedAt time.Time

	err := r.db.QueryRow(ctx, query, req.Id, req.Email, req.FirstName, req.LastName, req.PhoneNumber, req.IsActive).Scan(
		&user.Id,
		&user.Email,
		&user.FirstName,
		&user.LastName,
		&user.PhoneNumber,
		&user.IsActive,
		&createdAt,
		&updatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	user.CreatedAt = timestamppb.New(createdAt)
	user.UpdatedAt = timestamppb.New(updatedAt)

	return &user, nil
}

func (r *userRepository) Delete(ctx context.Context, id int64) error {
	query := `DELETE FROM users WHERE id = $1`

	result, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}

	return nil
}

func (r *userRepository) List(ctx context.Context, page, pageSize int32) ([]*pb.User, int32, error) {
	// Get total count
	var totalCount int32
	countQuery := `SELECT COUNT(*) FROM users`
	err := r.db.QueryRow(ctx, countQuery).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get user count: %w", err)
	}

	// Get users with pagination
	offset := (page - 1) * pageSize
	query := `
        SELECT id, email, first_name, last_name, phone_number, is_active, created_at, updated_at
        FROM users
        ORDER BY id DESC
        LIMIT $1 OFFSET $2
    `

	rows, err := r.db.Query(ctx, query, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users []*pb.User
	for rows.Next() {
		var user pb.User
		var createdAt, updatedAt time.Time

		err := rows.Scan(
			&user.Id,
			&user.Email,
			&user.FirstName,
			&user.LastName,
			&user.PhoneNumber,
			&user.IsActive,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan user: %w", err)
		}

		user.CreatedAt = timestamppb.New(createdAt)
		user.UpdatedAt = timestamppb.New(updatedAt)

		users = append(users, &user)
	}

	return users, totalCount, nil
}
