package app

import (
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/config"
	emailusecase "github.com/alfariesh/surau-backend/internal/usecase/email"
	"github.com/alfariesh/surau-backend/internal/usecase/notification"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/alfariesh/surau-backend/internal/usecase/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The registered-loop contract (F1-E): which supervised loops exist and which
// config gates each one. buildLoopSpecs is pure, so this locks the contract
// without any I/O. NOTE: the F1-E roadmap text says "four loops" — the fifth
// (email_events_poll, the Cloudflare event poller) landed later with F1-C, and
// the sixth (citable_unit_audit) with B-1.
func TestBuildLoopSpecsRegistersAllSixLoopsWhenEnabled(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	cfg.AuthCleanup.Enabled = true
	cfg.AuthCleanup.Interval = 6 * time.Hour
	cfg.OneSignal.Enabled = true
	cfg.OneSignal.ReminderInterval = time.Hour
	cfg.AuthAlert.Enabled = true
	cfg.AuthAlert.Interval = 15 * time.Minute
	cfg.CitableAudit.Enabled = true
	cfg.CitableAudit.Interval = time.Hour
	cfg.Email.DeliveryMode = config.EmailDeliveryModeCloudflare
	cfg.Email.DispatchInterval = 15 * time.Second
	cfg.Email.CloudflareEventPollingEnabled = true
	cfg.Email.CloudflareEventPollingInterval = time.Minute

	specs := buildLoopSpecs(cfg,
		&emailusecase.UseCase{}, &user.UseCase{}, &notification.UseCase{}, &unitregistry.UseCase{}, testLogger())

	names := loopNames(specs)
	require.Equal(t,
		[]string{"auth_cleanup", "notification_reminder", "auth_alert", "citable_unit_audit", "email_dispatch", "email_events_poll"},
		names, "the six supervised loops, in registration order")

	for _, spec := range specs {
		assert.NotNilf(t, spec.run, "loop %s must carry a pass function", spec.name)
		assert.Positivef(t, int64(spec.interval), "loop %s must have a positive interval", spec.name)
	}

	// Cleanup/reminder/alert/audit take the shared head start; email loops
	// tick on their own interval from the start (initialDelay zero).
	for _, spec := range specs[:4] {
		assert.Equalf(t, backgroundInitialDelay, spec.initialDelay, "loop %s initial delay", spec.name)
	}

	for _, spec := range specs[4:] {
		assert.Zerof(t, spec.initialDelay, "email loop %s must not take the head start", spec.name)
	}
}

func TestBuildLoopSpecsConfigGates(t *testing.T) {
	t.Parallel()

	base := func() *config.Config {
		cfg := &config.Config{}
		cfg.AuthCleanup.Enabled = true
		cfg.AuthCleanup.Interval = 6 * time.Hour
		cfg.Email.DeliveryMode = config.EmailDeliveryModeLog
		cfg.Email.DispatchInterval = 15 * time.Second

		return cfg
	}

	t.Run("default-shaped config runs cleanup and dispatch only", func(t *testing.T) {
		t.Parallel()

		specs := buildLoopSpecs(base(),
			&emailusecase.UseCase{}, &user.UseCase{}, &notification.UseCase{}, &unitregistry.UseCase{}, testLogger())
		assert.Equal(t, []string{"auth_cleanup", "email_dispatch"}, loopNames(specs))
	})

	t.Run("citable audit gate follows CitableAudit.Enabled", func(t *testing.T) {
		t.Parallel()

		cfg := base()
		cfg.CitableAudit.Enabled = true
		cfg.CitableAudit.Interval = time.Hour

		specs := buildLoopSpecs(cfg,
			&emailusecase.UseCase{}, &user.UseCase{}, &notification.UseCase{}, &unitregistry.UseCase{}, testLogger())
		assert.Equal(t, []string{"auth_cleanup", "citable_unit_audit", "email_dispatch"}, loopNames(specs))
	})

	t.Run("nil unit registry usecase drops the audit loop", func(t *testing.T) {
		t.Parallel()

		cfg := base()
		cfg.CitableAudit.Enabled = true
		cfg.CitableAudit.Interval = time.Hour

		specs := buildLoopSpecs(cfg,
			&emailusecase.UseCase{}, &user.UseCase{}, &notification.UseCase{}, nil, testLogger())
		assert.Equal(t, []string{"auth_cleanup", "email_dispatch"}, loopNames(specs))
	})

	t.Run("nil user usecase drops the auth loops", func(t *testing.T) {
		t.Parallel()

		cfg := base()
		cfg.AuthAlert.Enabled = true

		specs := buildLoopSpecs(cfg,
			&emailusecase.UseCase{}, nil, &notification.UseCase{}, &unitregistry.UseCase{}, testLogger())
		assert.Equal(t, []string{"email_dispatch"}, loopNames(specs))
	})

	t.Run("nil email usecase drops the email loops", func(t *testing.T) {
		t.Parallel()

		specs := buildLoopSpecs(base(),
			nil, &user.UseCase{}, &notification.UseCase{}, &unitregistry.UseCase{}, testLogger())
		assert.Equal(t, []string{"auth_cleanup"}, loopNames(specs))
	})

	t.Run("cloudflare mode without polling keeps the event poller off", func(t *testing.T) {
		t.Parallel()

		cfg := base()
		cfg.Email.DeliveryMode = config.EmailDeliveryModeCloudflare
		cfg.Email.CloudflareEventPollingEnabled = false

		specs := buildLoopSpecs(cfg,
			&emailusecase.UseCase{}, &user.UseCase{}, &notification.UseCase{}, &unitregistry.UseCase{}, testLogger())
		assert.Equal(t, []string{"auth_cleanup", "email_dispatch"}, loopNames(specs))
	})

	t.Run("log mode ignores the polling flag entirely", func(t *testing.T) {
		t.Parallel()

		cfg := base()
		cfg.Email.CloudflareEventPollingEnabled = true

		specs := buildLoopSpecs(cfg,
			&emailusecase.UseCase{}, &user.UseCase{}, &notification.UseCase{}, &unitregistry.UseCase{}, testLogger())
		assert.Equal(t, []string{"auth_cleanup", "email_dispatch"}, loopNames(specs))
	})
}

func loopNames(specs []loopSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.name)
	}

	return names
}
