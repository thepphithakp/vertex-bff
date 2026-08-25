package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/lru"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/vertex/bff/internal/client"
	"github.com/vertex/bff/internal/config"
	"github.com/vertex/bff/internal/graph"
	"github.com/vertex/bff/internal/loader"
)

const headerRequestID = "X-Request-Id"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("ตั้งค่าไม่ถูกต้อง", "err", err)
		os.Exit(1)
	}

	petSvc := client.NewPetClient(cfg.PetServiceURL, cfg.UpstreamTimeout)
	authSvc := client.NewAuthClient(cfg.AuthServiceURL, cfg.UpstreamTimeout)
	eventSvc := client.NewEventClient(cfg.EventServiceURL, cfg.UpstreamTimeout)

	resolver := &graph.Resolver{
		PetSvc: petSvc, AuthSvc: authSvc, EventSvc: eventSvc, Cfg: cfg,
	}

	srv := newGraphQLServer(resolver, cfg)

	app := fiber.New(fiber.Config{
		AppName:      "Vertex BFF",
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if fe, ok := err.(*fiber.Error); ok {
				code = fe.Code
			}
			return c.Status(code).JSON(fiber.Map{
				"error":     err.Error(),
				"requestId": c.Locals("requestID"),
			})
		},
	})

	app.Use(requestIDMiddleware)
	app.Use(accessLogMiddleware)

	app.Get("/livez", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"status": "ok"}) })
	app.Get("/readyz", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"status": "ok"}) })
	app.Get("/health", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"status": "ok"}) })

	// route เดียวของ service นี้
	//
	// proxy REST ชุดเดิม (/api/pets, /api/auth/*) ถูกเอาออกเพราะไม่มีใครเรียก
	// และ path ก็ไม่ตรงกับที่ทั้งสอง client ใช้จริง (/api/v1/...) อยู่แล้ว
	// การเก็บโค้ดที่ไม่ทำงานไว้ทำให้คนอ่านทีหลังเข้าใจผิดว่ามันใช้งานอยู่
	app.All("/graphql", adaptor.HTTPHandler(withRequestContext(srv, petSvc, authSvc)))

	slog.Info("BFF พร้อมรับงาน",
		"port", cfg.Port,
		"maxDepth", cfg.MaxQueryDepth,
		"maxComplexity", cfg.MaxQueryComplexity,
		"introspection", cfg.EnableIntrospection,
	)
	if err := app.Listen(":" + cfg.Port); err != nil {
		slog.Error("server หยุดทำงาน", "err", err)
		os.Exit(1)
	}
}

func newGraphQLServer(resolver *graph.Resolver, cfg config.Config) *handler.Server {
	srv := handler.New(graph.NewExecutableSchema(graph.Config{Resolvers: resolver}))

	srv.AddTransport(transport.POST{})
	srv.AddTransport(transport.GET{})
	srv.SetQueryCache(lru.New[*ast.QueryDocument](200))

	srv.Use(extension.AutomaticPersistedQuery{Cache: lru.New[string](100)})

	// ---------------------------------------------------------------------
	// เพดานความแพงของ query
	// ---------------------------------------------------------------------
	// rate limiter ที่นับจำนวน request ใช้กับ GraphQL แทบไม่ได้ผล
	// เพราะหนึ่ง request แพงเท่าไหร่ก็ได้ ยิง query ซ้อนลึกครั้งเดียว
	// ก็ทำให้ resolver ระเบิดเป็นพันครั้งได้ และ endpoint นี้เปิดออกเน็ต
	srv.Use(extension.FixedComplexityLimit(cfg.MaxQueryComplexity))

	if cfg.EnableIntrospection {
		srv.Use(extension.Introspection{})
	}

	// ---------------------------------------------------------------------
	// ชื่อ operation ต้องไปโผล่ใน log
	// ---------------------------------------------------------------------
	// ถ้าไม่ทำ ทุก request จะกลายเป็น POST /graphql เหมือนกันหมด
	// แล้วความสามารถกรองตาม endpoint ที่ทำไว้ใน VT-71 จะไร้ความหมาย
	srv.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		oc := graphql.GetOperationContext(ctx)

		// OperationName คือค่าที่ client ส่งมาใน request ซึ่งมักไม่ส่งมา
		// ชื่อจริงอยู่ในตัว document ที่ parse แล้ว ต้องอ่านจากตรงนั้น
		name := oc.OperationName
		if name == "" && oc.Operation != nil {
			name = oc.Operation.Name
		}
		if name == "" {
			// ปฏิเสธ query ที่ไม่มีชื่อ ไม่งั้นจะมี request ที่ระบุไม่ได้ว่ามาจากไหน
			return graphql.OneShot(graphql.ErrorResponse(ctx,
				"ทุก operation ต้องตั้งชื่อ เพื่อให้ตามหาใน log ได้ว่ามาจากหน้าไหน"))
		}
		start := time.Now()
		resp := next(ctx)
		return func(ctx context.Context) *graphql.Response {
			r := resp(ctx)
			slog.Info("graphql_operation",
				"endpoint", "graphql/"+name,
				"operation", name,
				"latency", time.Since(start),
				"request_id", client.RequestIDFrom(ctx),
				"errors", len(r.Errors),
			)
			return r
		}
	})

	srv.SetErrorPresenter(func(ctx context.Context, e error) *gqlerror.Error {
		err := graphql.DefaultErrorPresenter(ctx, e)
		if err.Extensions == nil {
			err.Extensions = map[string]any{}
		}
		if _, ok := err.Extensions["code"]; !ok {
			err.Extensions["code"] = "INTERNAL"
		}
		if rid := client.RequestIDFrom(ctx); rid != "" {
			err.Extensions["requestId"] = rid
		}
		return err
	})

	return srv
}

// withRequestContext ใส่ของที่ resolver ต้องใช้ลง context ของแต่ละ request
//
// สำคัญที่สุดคือ JWT — ส่งต่อไปให้ service ปลายทางเสมอ ไม่ให้ BFF ใช้สิทธิ์ตัวเอง
// การตรวจ token จริงยังเป็นหน้าที่ของ service ปลายทาง ที่นี่แค่ส่งผ่าน
func withRequestContext(next http.Handler, pets *client.PetClient, auth *client.AuthClient) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if h := r.Header.Get("Authorization"); h != "" {
			if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
				ctx = client.WithToken(ctx, h[7:])
			}
		}
		if rid := r.Header.Get(headerRequestID); rid != "" {
			ctx = client.WithRequestID(ctx, rid)
		}

		// loader ใหม่ต่อหนึ่ง request — ห้ามใช้ร่วมข้าม request
		// เพราะข้างในเก็บข้อมูลที่ผูกกับสิทธิ์ของผู้เรียกคนนั้น
		ctx = loader.With(ctx, loader.New(auth, pets))

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestIDMiddleware(c *fiber.Ctx) error {
	rid := c.Get(headerRequestID)
	if rid == "" {
		rid = uuid.NewString()
		c.Request().Header.Set(headerRequestID, rid)
	}
	c.Locals("requestID", rid)
	c.Set(headerRequestID, rid)
	return c.Next()
}

func accessLogMiddleware(c *fiber.Ctx) error {
	start := time.Now()
	err := c.Next()
	slog.Info("http_request",
		"method", c.Method(),
		"path", c.Path(),
		"status", c.Response().StatusCode(),
		"latency", time.Since(start),
		"request_id", c.Locals("requestID"),
	)
	return err
}
