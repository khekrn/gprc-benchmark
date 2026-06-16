// Package httpserver exposes the same REST surface as the Kotlin Routes —
// CRUD over /api/users plus an NDJSON streaming list, health, and /metrics —
// built on the Fiber v3 web framework (fasthttp).
package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/example/users-go/internal/domain"
	"github.com/example/users-go/internal/observability"
	"github.com/example/users-go/internal/service"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
)

type Server struct {
	svc     *service.UserService
	metrics *observability.Metrics
}

func New(svc *service.UserService, metrics *observability.Metrics) *Server {
	return &Server{svc: svc, metrics: metrics}
}

// App builds the routed, instrumented Fiber app.
func (s *Server) App() *fiber.App {
	app := fiber.New(fiber.Config{AppName: "users-go"})

	// Timing middleware: record latency by method + final status code.
	app.Use(func(c fiber.Ctx) error {
		started := time.Now()
		err := c.Next()
		s.metrics.ObserveHTTP(c.Method(), c.Response().StatusCode(), time.Since(started).Seconds())
		return err
	})

	app.Get("/healthz", func(c fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/readyz", func(c fiber.Ctx) error { return c.SendString("ok") })
	// Mount the Prometheus net/http handler through the Fiber adaptor.
	app.Get("/metrics", adaptor.HTTPHandler(s.metrics.Handler()))

	app.Get("/api/users/:id", s.handleGetUser)
	app.Post("/api/users", s.handleCreateUser)
	app.Post("/api/users/bulk", s.handleBulkCreate)
	app.Get("/api/users", s.handleStreamUsers)

	return app
}

type userJSON struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	FullName  string `json:"fullName"`
	CreatedAt string `json:"createdAt"`
}

func toJSON(u domain.User) userJSON {
	return userJSON{ID: u.ID, Email: u.Email, FullName: u.FullName, CreatedAt: u.CreatedAt.Format(time.RFC3339)}
}

type createBody struct {
	Email    string `json:"email"`
	FullName string `json:"fullName"`
}

func (s *Server) handleGetUser(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return problem(c, fiber.StatusBadRequest, "Bad Request", "id must be an integer")
	}
	u, err := s.svc.GetByID(c.Context(), id)
	if err != nil {
		var nf *domain.NotFoundError
		if errors.As(err, &nf) {
			return problem(c, fiber.StatusNotFound, "Not Found", err.Error())
		}
		return problem(c, fiber.StatusInternalServerError, "Internal Server Error", err.Error())
	}
	return c.Status(fiber.StatusOK).JSON(toJSON(u))
}

func (s *Server) handleCreateUser(c fiber.Ctx) error {
	var body createBody
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return problem(c, fiber.StatusBadRequest, "Bad Request", "json expected")
	}
	in, err := domain.MakeNewUser(body.Email, body.FullName)
	if err != nil {
		return problem(c, fiber.StatusBadRequest, "Bad Request", err.Error())
	}
	u, err := s.svc.Create(c.Context(), in)
	if err != nil {
		var dup *domain.DuplicateEmailError
		if errors.As(err, &dup) {
			return problem(c, fiber.StatusConflict, "Conflict", err.Error())
		}
		return problem(c, fiber.StatusInternalServerError, "Internal Server Error", err.Error())
	}
	return c.Status(fiber.StatusCreated).JSON(toJSON(u))
}

func (s *Server) handleBulkCreate(c fiber.Ctx) error {
	var arr []createBody
	if err := json.Unmarshal(c.Body(), &arr); err != nil {
		return problem(c, fiber.StatusBadRequest, "Bad Request", "array expected")
	}
	inputs := make([]domain.NewUser, 0, len(arr))
	for _, e := range arr {
		in, err := domain.MakeNewUser(e.Email, e.FullName)
		if err != nil {
			return problem(c, fiber.StatusBadRequest, "Bad Request", err.Error())
		}
		inputs = append(inputs, in)
	}
	created, err := s.svc.BulkCreate(c.Context(), inputs)
	if err != nil {
		return problem(c, fiber.StatusInternalServerError, "Internal Server Error", err.Error())
	}
	out := make([]userJSON, len(created))
	for i, u := range created {
		out[i] = toJSON(u)
	}
	return c.Status(fiber.StatusCreated).JSON(out)
}

// handleStreamUsers writes NDJSON (one JSON object per line), flushing each row.
// The flush blocks when the client is slow, back-pressuring the PG cursor in
// StreamAll. We detach from the request context because fasthttp recycles the
// RequestCtx once this handler returns, before the stream writer runs.
func (s *Server) handleStreamUsers(c fiber.Ctx) error {
	var prefix *string
	if p := c.Query("emailPrefix"); p != "" {
		prefix = &p
	}
	c.Set("Content-Type", "application/x-ndjson")
	streamCtx := context.WithoutCancel(c.Context())

	return c.SendStreamWriter(func(w *bufio.Writer) {
		_ = s.svc.StreamAll(streamCtx, prefix, func(u domain.User) error {
			line, err := json.Marshal(toJSON(u))
			if err != nil {
				return err
			}
			if _, err := w.Write(append(line, '\n')); err != nil {
				return err
			}
			return w.Flush()
		})
	})
}

// -------- helpers ---------------------------------------------------------

func problem(c fiber.Ctx, status int, title, detail string) error {
	p := fiber.Map{"type": "about:blank", "title": title, "status": status}
	if detail != "" {
		p["detail"] = detail
	}
	// JSON's optional second arg sets a custom content type (RFC 7807).
	return c.Status(status).JSON(p, "application/problem+json")
}
