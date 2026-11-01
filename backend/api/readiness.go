package api

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/internal/lifecycle"
)

// readinessTimeout bounds one readiness ping, the wait for a pooled
// connection included (PAC-81 AC-A11).
var readinessTimeout = 2 * time.Second

// readiness answers GET /api/ready (getReadiness). It is public and outside
// every abuse limit, so concurrent calls share one database ping (single
// flight): at most one ping is in flight per process and readiness never
// holds more than one pooled connection (contract revision 2, B2).
type readiness struct {
	ping         func(ctx context.Context) error
	shuttingDown func() bool

	mu     sync.Mutex
	flight *pingFlight
}

type pingFlight struct {
	done chan struct{}
	err  error
}

func newReadiness(db *sql.DB) *readiness {
	return &readiness{
		ping:         func(ctx context.Context) error { return db.PingContext(ctx) },
		shuttingDown: func() bool { return lifecycle.Default().ShuttingDown() },
	}
}

// ready reports whether the process is ready. The shutdown flag is read
// before starting or joining a ping, and again when the answer is chosen.
func (rd *readiness) ready() bool {
	if rd.shuttingDown() {
		return false
	}
	rd.mu.Lock()
	f := rd.flight
	if f == nil {
		f = &pingFlight{done: make(chan struct{})}
		rd.flight = f
		go rd.run(f)
	}
	rd.mu.Unlock()
	<-f.done
	if f.err != nil {
		log.Printf("readiness: database not ready: %v", f.err)
		return false
	}
	return !rd.shuttingDown()
}

// run performs one ping on a context that belongs to no request, so one
// caller going away cannot cancel the others' answer.
func (rd *readiness) run(f *pingFlight) {
	ctx, cancel := context.WithTimeout(context.Background(), readinessTimeout)
	defer cancel()
	f.err = rd.ping(ctx)
	rd.mu.Lock()
	rd.flight = nil
	rd.mu.Unlock()
	close(f.done)
}

// GetReadiness implements getReadiness on the generated strict wrapper.
func (s *generatedServer) GetReadiness(_ context.Context, _ gen.GetReadinessRequestObject) (gen.GetReadinessResponseObject, error) {
	if s.ready.ready() {
		return gen.GetReadiness200JSONResponse{Status: gen.Ready}, nil
	}
	return gen.GetReadiness503JSONResponse{Error: gen.NotReady}, nil
}

// readinessHandler mounts getReadiness through the generated strict wrapper.
func readinessHandler(rd *readiness) http.HandlerFunc {
	h := gen.NewStrictHandlerWithOptions(&generatedServer{ready: rd}, []gen.StrictMiddlewareFunc{withRequest}, strictOptions)
	return h.GetReadiness
}
