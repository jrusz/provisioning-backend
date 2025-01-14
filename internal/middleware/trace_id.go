package middleware

import (
	"net/http"

	"github.com/RHEnVision/provisioning-backend/internal/logging"
	"github.com/RHEnVision/provisioning-backend/internal/random"
	"go.opentelemetry.io/otel/trace"
)

func TraceID(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Edge request id
		edgeId := r.Header.Get("X-Rh-Edge-Request-Id")
		if edgeId != "" {
			ctx = logging.WithEdgeRequestId(ctx, edgeId)
		}

		// OpenTelemetry trace id
		traceId := trace.SpanFromContext(ctx).SpanContext().TraceID()
		if !traceId.IsValid() {
			// OpenTelemetry library does not provide a public interface to create new IDs
			traceId = random.TraceID()
		}

		// Store in response headers for easier debugging
		w.Header().Set("X-Trace-Id", traceId.String())

		ctx = logging.WithTraceId(ctx, traceId.String())
		next.ServeHTTP(w, r.WithContext(ctx))
	}
	return http.HandlerFunc(fn)
}
