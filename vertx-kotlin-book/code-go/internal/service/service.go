// Package service is the thin application-service layer. Business logic that
// doesn't belong in the repository (validation orchestration, cross-aggregate
// checks) lives here, keeping it testable without a database.
package service

import (
	"context"

	"github.com/example/users-go/internal/db"
	"github.com/example/users-go/internal/domain"
)

type UserService struct {
	repo *db.Repository
}

func New(repo *db.Repository) *UserService { return &UserService{repo: repo} }

func (s *UserService) GetByID(ctx context.Context, id int64) (domain.User, error) {
	u, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	if u == nil {
		return domain.User{}, &domain.NotFoundError{ID: id}
	}
	return *u, nil
}

func (s *UserService) Create(ctx context.Context, in domain.NewUser) (domain.User, error) {
	// Friendly uniqueness pre-check so we can return a clean 409 without
	// relying on the DB error; still catch DuplicateEmail from the repo
	// because races exist.
	existing, err := s.repo.FindByEmail(ctx, in.Email)
	if err != nil {
		return domain.User{}, err
	}
	if existing != nil {
		return domain.User{}, &domain.DuplicateEmailError{Email: in.Email}
	}
	return s.repo.Create(ctx, in)
}

func (s *UserService) BulkCreate(ctx context.Context, inputs []domain.NewUser) ([]domain.User, error) {
	return s.repo.CreateMany(ctx, inputs)
}

// StreamAll yields each user to fn; a non-nil fn error stops the stream.
func (s *UserService) StreamAll(ctx context.Context, emailPrefix *string, fn func(domain.User) error) error {
	return s.repo.StreamAll(ctx, emailPrefix, 100, fn)
}
