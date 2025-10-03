package repository

import (
	"context"

	pb "coding2fun.in/grpc-users/pkg/user/v1"
)

type UserRepository interface {
	Create(ctx context.Context, req *pb.CreateUserRequest) (*pb.User, error)
	GetByID(ctx context.Context, id int64) (*pb.User, error)
	Update(ctx context.Context, req *pb.UpdateUserRequest) (*pb.User, error)
	Delete(ctx context.Context, id int64) error
	List(ctx context.Context, page, pageSize int32) ([]*pb.User, int32, error)
}
