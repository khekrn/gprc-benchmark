// Package grpcserver implements the generated usersv1.UsersServer covering
// every RPC style: unary, server streaming, client streaming, bidi.
package grpcserver

import (
	"context"
	"errors"
	"io"
	"time"

	usersv1 "github.com/example/users-go/gen/usersv1"
	"github.com/example/users-go/internal/domain"
	"github.com/example/users-go/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	usersv1.UnimplementedUsersServer
	svc *service.UserService
}

func New(svc *service.UserService) *Server { return &Server{svc: svc} }

func toReply(u domain.User) *usersv1.UserReply {
	return &usersv1.UserReply{
		Id:        u.ID,
		Email:     u.Email,
		FullName:  u.FullName,
		CreatedAt: u.CreatedAt.Format(time.RFC3339),
	}
}

// mapErr translates domain errors to gRPC status codes (the Go analogue of the
// Kotlin StatusException mapping).
func mapErr(err error) error {
	var nf *domain.NotFoundError
	var dup *domain.DuplicateEmailError
	var val *domain.ValidationError
	switch {
	case errors.As(err, &nf):
		return status.Error(codes.NotFound, err.Error())
	case errors.As(err, &dup):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.As(err, &val):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

// ----- Unary --------------------------------------------------------------

func (s *Server) GetUser(ctx context.Context, req *usersv1.GetUserRequest) (*usersv1.UserReply, error) {
	u, err := s.svc.GetByID(ctx, req.GetId())
	if err != nil {
		return nil, mapErr(err)
	}
	return toReply(u), nil
}

func (s *Server) CreateUser(ctx context.Context, req *usersv1.CreateUserRequest) (*usersv1.UserReply, error) {
	in, err := domain.MakeNewUser(req.GetEmail(), req.GetFullName())
	if err != nil {
		return nil, mapErr(err)
	}
	u, err := s.svc.Create(ctx, in)
	if err != nil {
		return nil, mapErr(err)
	}
	return toReply(u), nil
}

// ----- Server streaming ---------------------------------------------------

func (s *Server) ListUsers(req *usersv1.ListUsersRequest, stream usersv1.Users_ListUsersServer) error {
	var prefix *string
	if p := req.GetEmailPrefix(); p != "" {
		prefix = &p
	}
	// stream.Send blocks on HTTP/2 flow control, so a slow client back-pressures
	// the cursor all the way to Postgres — end-to-end, no buffer explodes.
	return s.svc.StreamAll(stream.Context(), prefix, func(u domain.User) error {
		return stream.Send(toReply(u))
	})
}

// ----- Client streaming ---------------------------------------------------

func (s *Server) ImportUsers(stream usersv1.Users_ImportUsersServer) error {
	var imported, skipped int64
	var errs []string
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break // client half-closed
		}
		if err != nil {
			return err
		}
		in, verr := domain.MakeNewUser(req.GetEmail(), req.GetFullName())
		if verr != nil {
			skipped++
			errs = append(errs, verr.Error())
			continue
		}
		if _, cerr := s.svc.Create(stream.Context(), in); cerr != nil {
			skipped++
			var dup *domain.DuplicateEmailError
			if !errors.As(cerr, &dup) {
				errs = append(errs, cerr.Error())
			}
			continue
		}
		imported++
	}
	return stream.SendAndClose(&usersv1.ImportSummary{
		Imported: imported,
		Skipped:  skipped,
		Errors:   errs,
	})
}

// ----- Bidirectional streaming --------------------------------------------

func (s *Server) Chat(stream usersv1.Users_ChatServer) error {
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil // client half-closed; end our half
		}
		if err != nil {
			return err
		}
		reply := &usersv1.ChatMessage{
			From:     "server",
			Text:     "echo: " + msg.GetText(),
			TsMillis: time.Now().UnixMilli(),
		}
		if err := stream.Send(reply); err != nil {
			return err
		}
	}
}
