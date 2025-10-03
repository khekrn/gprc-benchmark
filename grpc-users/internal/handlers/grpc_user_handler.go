package handler

import (
	"context"

	"coding2fun.in/grpc-users/internal/service"
	pb "coding2fun.in/grpc-users/pkg/user/v1"
	"go.uber.org/zap"
)

// GRPCUserHandler implements the gRPC UserServiceServer interface
type GRPCUserHandler struct {
	pb.UnimplementedUserServiceServer
	userService service.UserService
	logger      *zap.Logger
}

func NewGRPCUserHandler(userService service.UserService, logger *zap.Logger) *GRPCUserHandler {
	return &GRPCUserHandler{
		userService: userService,
		logger:      logger,
	}
}

func (h *GRPCUserHandler) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.CreateUserResponse, error) {
	h.logger.Info("gRPC CreateUser called",
		zap.String("email", req.Email),
		zap.String("first_name", req.FirstName),
		zap.String("last_name", req.LastName))

	resp, err := h.userService.CreateUser(ctx, req)
	if err != nil {
		h.logger.Error("gRPC CreateUser failed", zap.Error(err))
		return nil, err
	}

	h.logger.Info("gRPC CreateUser successful", zap.Int64("user_id", resp.User.Id))
	return resp, nil
}

func (h *GRPCUserHandler) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.GetUserResponse, error) {
	h.logger.Info("gRPC GetUser called", zap.Int64("user_id", req.Id))

	resp, err := h.userService.GetUser(ctx, req)
	if err != nil {
		h.logger.Error("gRPC GetUser failed", zap.Int64("user_id", req.Id), zap.Error(err))
		return nil, err
	}

	h.logger.Info("gRPC GetUser successful", zap.Int64("user_id", req.Id))
	return resp, nil
}

func (h *GRPCUserHandler) UpdateUser(ctx context.Context, req *pb.UpdateUserRequest) (*pb.UpdateUserResponse, error) {
	h.logger.Info("gRPC UpdateUser called",
		zap.Int64("user_id", req.Id),
		zap.String("email", req.Email),
		zap.String("first_name", req.FirstName),
		zap.String("last_name", req.LastName))

	resp, err := h.userService.UpdateUser(ctx, req)
	if err != nil {
		h.logger.Error("gRPC UpdateUser failed", zap.Int64("user_id", req.Id), zap.Error(err))
		return nil, err
	}

	h.logger.Info("gRPC UpdateUser successful", zap.Int64("user_id", req.Id))
	return resp, nil
}

func (h *GRPCUserHandler) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserResponse, error) {
	h.logger.Info("gRPC DeleteUser called", zap.Int64("user_id", req.Id))

	resp, err := h.userService.DeleteUser(ctx, req)
	if err != nil {
		h.logger.Error("gRPC DeleteUser failed", zap.Int64("user_id", req.Id), zap.Error(err))
		return nil, err
	}

	h.logger.Info("gRPC DeleteUser successful", zap.Int64("user_id", req.Id))
	return resp, nil
}

func (h *GRPCUserHandler) ListUsers(ctx context.Context, req *pb.ListUsersRequest) (*pb.ListUsersResponse, error) {
	h.logger.Info("gRPC ListUsers called",
		zap.Int32("page", req.Page),
		zap.Int32("page_size", req.PageSize))

	resp, err := h.userService.ListUsers(ctx, req)
	if err != nil {
		h.logger.Error("gRPC ListUsers failed", zap.Error(err))
		return nil, err
	}

	h.logger.Info("gRPC ListUsers successful",
		zap.Int32("total_count", resp.TotalCount),
		zap.Int("users_returned", len(resp.Users)))
	return resp, nil
}
