package service

import (
	"context"
	"fmt"
	"strings"

	"coding2fun.in/grpc-users/internal/repository"
	pb "coding2fun.in/grpc-users/pkg/user/v1"
	"go.uber.org/zap"
)

type userService struct {
	userRepo repository.UserRepository
	logger   *zap.Logger
}

func NewUserService(userRepo repository.UserRepository, logger *zap.Logger) UserService {
	return &userService{
		userRepo: userRepo,
		logger:   logger,
	}
}

func (s *userService) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.CreateUserResponse, error) {
	// Validate request
	if err := s.validateCreateUserRequest(req); err != nil {
		s.logger.Error("Invalid create user request", zap.Error(err))
		return nil, err
	}

	user, err := s.userRepo.Create(ctx, req)
	if err != nil {
		s.logger.Error("Failed to create user", zap.Error(err))
		return nil, err
	}

	s.logger.Info("User created successfully", zap.Int64("user_id", user.Id))

	return &pb.CreateUserResponse{User: user}, nil
}

func (s *userService) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.GetUserResponse, error) {
	if req.Id <= 0 {
		return nil, fmt.Errorf("invalid user ID")
	}

	user, err := s.userRepo.GetByID(ctx, req.Id)
	if err != nil {
		s.logger.Error("Failed to get user", zap.Int64("user_id", req.Id), zap.Error(err))
		return nil, err
	}

	return &pb.GetUserResponse{User: user}, nil
}

func (s *userService) UpdateUser(ctx context.Context, req *pb.UpdateUserRequest) (*pb.UpdateUserResponse, error) {
	if err := s.validateUpdateUserRequest(req); err != nil {
		s.logger.Error("Invalid update user request", zap.Error(err))
		return nil, err
	}

	user, err := s.userRepo.Update(ctx, req)
	if err != nil {
		s.logger.Error("Failed to update user", zap.Int64("user_id", req.Id), zap.Error(err))
		return nil, err
	}

	s.logger.Info("User updated successfully", zap.Int64("user_id", user.Id))

	return &pb.UpdateUserResponse{User: user}, nil
}

func (s *userService) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserResponse, error) {
	if req.Id <= 0 {
		return nil, fmt.Errorf("invalid user ID")
	}

	err := s.userRepo.Delete(ctx, req.Id)
	if err != nil {
		s.logger.Error("Failed to delete user", zap.Int64("user_id", req.Id), zap.Error(err))
		return nil, err
	}

	s.logger.Info("User deleted successfully", zap.Int64("user_id", req.Id))

	return &pb.DeleteUserResponse{Success: true}, nil
}

func (s *userService) ListUsers(ctx context.Context, req *pb.ListUsersRequest) (*pb.ListUsersResponse, error) {
	// Set default pagination values
	page := req.Page
	if page <= 0 {
		page = 1
	}

	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100 // Limit max page size
	}

	users, totalCount, err := s.userRepo.List(ctx, page, pageSize)
	if err != nil {
		s.logger.Error("Failed to list users", zap.Error(err))
		return nil, err
	}

	return &pb.ListUsersResponse{
		Users:      users,
		TotalCount: totalCount,
		Page:       page,
		PageSize:   pageSize,
	}, nil
}

func (s *userService) validateCreateUserRequest(req *pb.CreateUserRequest) error {
	if strings.TrimSpace(req.Email) == "" {
		return fmt.Errorf("email is required")
	}
	if strings.TrimSpace(req.FirstName) == "" {
		return fmt.Errorf("first name is required")
	}
	if strings.TrimSpace(req.LastName) == "" {
		return fmt.Errorf("last name is required")
	}
	return nil
}

func (s *userService) validateUpdateUserRequest(req *pb.UpdateUserRequest) error {
	if req.Id <= 0 {
		return fmt.Errorf("invalid user ID")
	}
	if strings.TrimSpace(req.Email) == "" {
		return fmt.Errorf("email is required")
	}
	if strings.TrimSpace(req.FirstName) == "" {
		return fmt.Errorf("first name is required")
	}
	if strings.TrimSpace(req.LastName) == "" {
		return fmt.Errorf("last name is required")
	}
	return nil
}
