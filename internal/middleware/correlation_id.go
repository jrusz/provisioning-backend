package middleware

import (
	"net/http"

	"github.com/RHEnVision/provisioning-backend/internal/ctxval"
)

func CorrelationID(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		corrId := r.Header.Get("X-Correlation-Id")
		if corrId != "" {
			ctx = ctxval.WithCorrelationId(ctx, corrId)
			// Store in response headers for easier debugging
			w.Header().Set("X-Correlation-Id", corrId)
			logger := ctxval.Logger(ctx).With().Str("correlation_id", corrId).Logger()
			logger.Trace().Msgf("Added correlation id %s to logger", corrId)
			ctx = ctxval.WithLogger(ctx, &logger)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	}
	return http.HandlerFunc(fn)
}