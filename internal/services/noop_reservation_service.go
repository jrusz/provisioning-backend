package services

import (
	"net/http"

	"github.com/RHEnVision/provisioning-backend/internal/dao"
	"github.com/RHEnVision/provisioning-backend/internal/identity"
	"github.com/RHEnVision/provisioning-backend/internal/jobs"
	"github.com/RHEnVision/provisioning-backend/internal/models"
	"github.com/RHEnVision/provisioning-backend/internal/payloads"
	"github.com/RHEnVision/provisioning-backend/internal/queue"
	"github.com/RHEnVision/provisioning-backend/pkg/worker"
	"github.com/go-chi/render"
	"github.com/rs/zerolog"
)

// CreateNoopReservation is used to create empty reservation that is processed without any operation
// being made. This is useful when testing the job queue. The endpoint has no payload.
func CreateNoopReservation(w http.ResponseWriter, r *http.Request) {
	logger := zerolog.Ctx(r.Context())
	accountId := identity.AccountId(r.Context())
	identity := identity.Identity(r.Context())
	rDao := dao.GetReservationDao(r.Context())
	reservation := &models.NoopReservation{
		Reservation: models.Reservation{
			Provider:   models.ProviderTypeNoop,
			AccountID:  accountId,
			Status:     "Created",
			Steps:      1,
			StepTitles: []string{"A test step"},
		},
	}

	// create reservation in the database
	err := rDao.CreateNoop(r.Context(), reservation)
	if err != nil {
		renderError(w, r, payloads.NewDAOError(r.Context(), "create noop reservation", err))
		return
	}
	logger.Debug().Msgf("Created a new reservation %d", reservation.ID)

	// create a new job
	pj := worker.Job{
		Type:      jobs.TypeNoop,
		AccountID: accountId,
		Identity:  identity,
		Args: jobs.NoopJobArgs{
			ReservationID: reservation.ID,
		},
	}
	err = queue.GetEnqueuer(r.Context()).Enqueue(r.Context(), &pj)
	if err != nil {
		renderError(w, r, payloads.NewEnqueueTaskError(r.Context(), "job enqueue error", err))
		return
	}

	if err := render.Render(w, r, payloads.NewNoopReservationResponse(reservation)); err != nil {
		renderError(w, r, payloads.NewRenderError(r.Context(), "unable to render reservation", err))
	}
}
