// Package httpapi exposes the documented Helm HTTP API and the embedded
// frontend fallback used when a separate web/dist bundle is unavailable.
package httpapi

import (
	"context"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/betaswitch"
	"github.com/KanterLabs/helm/internal/codexruntime"
	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/publicendpoint"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"sync"
)

type Server struct {
	Store *store.Store
	Auth  *auth.Manager
	Cfg   config.Config
	Codex CodexAccountService
	// BetaSwitch is an injected client for the root-owned beta switch broker.
	// The HTTP layer never invokes deployment commands or accesses release
	// paths directly.
	BetaSwitch betaswitch.Client
	// PublicEndpoints provisions and runs the optional Cloudflare tunnel
	// that exposes webhook routes publicly; nil when not configured.
	PublicEndpoints *publicendpoint.Manager
	idemMu          sync.Mutex
	betaSwitchIdem  map[string]betaSwitchReplay
	// mutationLimiter is initialized by New and is intentionally process-local.
	// Persistent agent accounting lives in store so a restart cannot reset the
	// actor's resource budget.
	mutationLimiter             *mutationRateLimiter
	mutationLimiterOnce         sync.Once
	agentRequestLimiter         *mutationRateLimiter
	agentRequestLimiterOnce     sync.Once
	bearerCredentialLimiter     *mutationRateLimiter
	bearerCredentialLimiterOnce sync.Once
	bodyBufferPool              *bodyBufferPool
	bearerAuthSlots             chan struct{}
	metrics                     *metricsRegistry
	metricsOnce                 sync.Once
	adminActivity               *adminActivityTracker
	adminActivityOnce           sync.Once
	projectIntelligenceMu       sync.Mutex
	projectIntelligenceCache    map[string]projectIntelligenceCacheEntry
	projectIntelligenceRunning  map[string]bool
	static                      http.Handler
}

type CodexAccountService interface {
	Account(context.Context, string, bool) (codexruntime.AccountStatus, error)
	StartDeviceLogin(context.Context, string) (codexruntime.DeviceLogin, error)
	CancelDeviceLogin(context.Context, string, string) (codexruntime.CancelLoginResult, error)
	LogoutAccount(context.Context, string) error
}

func New(s *store.Store, manager *auth.Manager, cfg config.Config, codexManagers ...CodexAccountService) *Server {
	return NewWithBetaSwitch(s, manager, cfg, nil, codexManagers...)
}

// NewWithBetaSwitch constructs a server with an optional beta switch broker
// client. Keeping the client separate from the existing Codex variadic
// dependency preserves the constructor used by existing callers and tests.
func NewWithBetaSwitch(s *store.Store, manager *auth.Manager, cfg config.Config, betaClient betaswitch.Client, codexManagers ...CodexAccountService) *Server {
	var codexManager CodexAccountService
	if len(codexManagers) > 0 {
		codexManager = codexManagers[0]
	}
	return &Server{Store: s, Auth: manager, Cfg: cfg, Codex: codexManager, BetaSwitch: betaClient, betaSwitchIdem: make(map[string]betaSwitchReplay), mutationLimiter: newDefaultMutationRateLimiter(), agentRequestLimiter: newDefaultAgentRequestLimiter(), bearerCredentialLimiter: newDefaultBearerCredentialLimiter(), bodyBufferPool: processBodyBufferPool, bearerAuthSlots: processBearerAuthSlots, metrics: newMetricsRegistry(), adminActivity: newAdminActivityTracker(), projectIntelligenceCache: make(map[string]projectIntelligenceCacheEntry), projectIntelligenceRunning: make(map[string]bool)}
}
