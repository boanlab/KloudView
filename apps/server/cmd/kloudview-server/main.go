package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kloudview/kloudview/apps/server/internal/access"
	"github.com/kloudview/kloudview/apps/server/internal/api"
	"github.com/kloudview/kloudview/apps/server/internal/auth"
	"github.com/kloudview/kloudview/apps/server/internal/domain"
	"github.com/kloudview/kloudview/apps/server/internal/store"
)

var version = "dev"

func main() {
	addr := getenv("KLOUDVIEW_ADDR", ":8080")
	token := os.Getenv("KLOUDVIEW_ENROLLMENT_TOKEN")
	credentialKey := os.Getenv("KLOUDVIEW_AGENT_CREDENTIAL_KEY")
	if err := validateServerSecrets(token, credentialKey); err != nil {
		slog.Error("invalid server secrets", "error", err)
		os.Exit(1)
	}
	webRoot := os.Getenv("KLOUDVIEW_WEB_ROOT")
	statePath := os.Getenv("KLOUDVIEW_STATE_PATH")
	accessPath := os.Getenv("KLOUDVIEW_ACCESS_STATE_PATH")
	databaseURL := os.Getenv("KLOUDVIEW_DATABASE_URL")
	var database *store.Postgres
	var databaseAccess []byte
	var memory *store.Memory
	var err error
	if databaseURL != "" {
		startupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		database, err = connectPostgres(startupCtx, databaseURL)
		cancel()
		if err == nil {
			memory, databaseAccess, err = database.Load(context.Background())
		}
	} else {
		memory, err = loadState(statePath)
	}
	if err != nil {
		slog.Error("state load failed", "error", err)
		os.Exit(1)
	}
	if database != nil {
		defer database.Close()
	}
	accessEngine := access.NewEngine()
	if len(databaseAccess) > 0 {
		loaded, err := access.RestoreState(databaseAccess)
		if err != nil {
			slog.Error("access state load failed", "error", err)
			os.Exit(1)
		}
		accessEngine = loaded
	} else if database == nil && accessPath != "" {
		loaded, err := access.NewPersistent(accessPath)
		if err != nil {
			slog.Error("access state load failed", "error", err)
			os.Exit(1)
		}
		accessEngine = loaded
	}
	type seedUser struct{ username, display, password string }
	seedUsers := []seedUser{{"admin", "Administrator", getenv("KLOUDVIEW_ADMIN_PASSWORD", "admin")}}
	for _, seed := range seedUsers {
		existing, exists := memory.UserByUsername(seed.username)
		if exists && existing.PasswordHash != "" {
			continue // already provisioned with a usable credential
		}
		hash, herr := auth.HashPassword(seed.password)
		if herr != nil {
			slog.Error("user seed failed", "username", seed.username, "error", herr)
			os.Exit(1)
		}
		now := time.Now().UTC()
		user := domain.User{ID: "user-" + seed.username, Username: seed.username, DisplayName: seed.display, PasswordHash: hash, Status: "active", CreatedAt: now, UpdatedAt: now}
		if exists { // a record without a usable credential keeps its identity
			user.ID = existing.ID
			user.DisplayName = existing.DisplayName
			user.Status = existing.Status
			user.CreatedAt = existing.CreatedAt
		}
		memory.PutUser(user)
		slog.Info("seeded user", "username", seed.username)
	}
	// Header-based subject fallback is an internal test/automation affordance and
	// is OFF unless explicitly enabled. Production authenticates via session cookie.
	devHeaderAuth := getenv("KLOUDVIEW_DEV_HEADER_AUTH", "false") == "true"
	// An unhardened deployment is indistinguishable from a hardened one at
	// runtime, so each unmet checklist item is named at startup.
	for _, warning := range hardeningWarnings(hardeningConfig{
		AdminPassword: getenv("KLOUDVIEW_ADMIN_PASSWORD", defaultAdminPassword),
		DevHeaderAuth: devHeaderAuth,
		PublicURL:     os.Getenv("KLOUDVIEW_PUBLIC_URL"),
	}) {
		slog.Warn("not production hardened", "item", warning)
	}
	apiServer := api.NewWithAccess(memory, accessEngine, token, webRoot).WithAgentCredentialKey(credentialKey).WithVersion(version).WithDevHeaderAuth(devHeaderAuth).WithAgentReleases(os.Getenv("KLOUDVIEW_AGENT_RELEASE_PATH"), os.Getenv("KLOUDVIEW_AGENT_TARGET_VERSION")).
		WithAgentRollout(os.Getenv("KLOUDVIEW_AGENT_CANARY"), canarySoak())
	if database != nil {
		apiServer.WithStorageHealth(database.Ping).WithMetricHistory(database.MetricRange)
	}
	server := &http.Server{Addr: addr, Handler: apiServer.Handler(), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go apiServer.WatchConnectivity(ctx)
	var persistDone chan struct{}
	if statePath != "" || database != nil {
		persistDone = make(chan struct{})
		go func() {
			defer close(persistDone)
			persist(ctx, memory, accessEngine, database, statePath, accessPath)
		}()
	}
	errors := make(chan error, 1)
	slog.Info("server started", "address", addr)
	go func() { errors <- server.ListenAndServe() }()
	var serverErr error
	select {
	case err := <-errors:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("server stopped", "error", err)
			serverErr = err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("server shutdown failed", "error", err)
		}
	}
	stop()
	if persistDone != nil {
		<-persistDone
	}
	finalSaveCtx, cancelFinalSave := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFinalSave()
	if err := saveState(finalSaveCtx, memory, accessEngine, database, statePath, accessPath); err != nil {
		slog.Error("state save failed", "error", err)
	}
	if serverErr != nil {
		os.Exit(1)
	}
}

// publishedSecrets are the placeholder and compose-default values. They are long
// enough to satisfy the length rules, so without naming them a deployment that
// copied .env.example unedited starts on secrets that are in the repository.
var publishedSecrets = map[string]bool{
	"replace-with-random-bootstrap-token":              true,
	"replace-with-independent-random-server-key":       true,
	"replace-with-random-database-password":            true,
	"change-me-admin-password":                         true,
	"local-development-token-change-me":                true,
	"local-development-agent-credential-key-change-me": true,
	"local-development-postgres-password":              true,
}

func validateServerSecrets(enrollmentToken, credentialKey string) error {
	if len(enrollmentToken) < 16 {
		return errors.New("enrollment token must contain at least 16 characters")
	}
	if len(credentialKey) < 32 {
		return errors.New("agent credential key must contain at least 32 characters")
	}
	if credentialKey == enrollmentToken {
		return errors.New("agent credential key must differ from enrollment token")
	}
	if publishedSecrets[enrollmentToken] {
		return errors.New("enrollment token is a published placeholder; set it to a random value")
	}
	if publishedSecrets[credentialKey] {
		return errors.New("agent credential key is a published placeholder; set it to a random value")
	}
	return nil
}

func loadState(path string) (*store.Memory, error) {
	if path == "" {
		return store.NewMemory(), nil
	}
	if _, err := os.Stat(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return store.NewPersistent(path)
}

func persist(ctx context.Context, memory *store.Memory, accessEngine *access.Engine, database *store.Postgres, path, accessPath string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := saveState(ctx, memory, accessEngine, database, path, accessPath); err != nil {
				slog.Error("state save failed", "error", err)
			}
		}
	}
}

func saveState(ctx context.Context, memory *store.Memory, accessEngine *access.Engine, database *store.Postgres, path, accessPath string) error {
	if database != nil {
		accessData, err := accessEngine.MarshalState()
		if err != nil {
			return err
		}
		return database.Save(ctx, memory, accessData)
	}
	if path != "" {
		if err := memory.Save(path); err != nil {
			return err
		}
	}
	if accessPath != "" {
		return accessEngine.Save(accessPath)
	}
	return nil
}

func connectPostgres(ctx context.Context, databaseURL string) (*store.Postgres, error) {
	var lastErr error
	for ctx.Err() == nil {
		database, err := store.OpenPostgres(ctx, databaseURL)
		if err == nil {
			return database.WithRetention(
				getenvInt("KLOUDVIEW_METRIC_RAW_DAYS", 30),
				getenvInt("KLOUDVIEW_METRIC_ROLLUP_DAYS", 400),
				getenvInt("KLOUDVIEW_METRIC_ROLLUP_SECONDS", 60),
			), nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	return nil, lastErr
}

func getenvInt(key string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(key)); err == nil && value > 0 {
		return value
	}
	return fallback
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// canarySoak is how long the canary must hold a new agent build before the rest
// of the fleet is offered it. Zero leaves the choice to the rollout default.
func canarySoak() time.Duration {
	value, err := time.ParseDuration(os.Getenv("KLOUDVIEW_AGENT_CANARY_SOAK"))
	if err != nil || value <= 0 {
		return 0
	}
	return value
}
