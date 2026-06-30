package webserver

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/jellydator/ttlcache/v3"
	wrate "github.com/webriots/rate"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/metrics"
	plugincache "github.com/shareed2k/honey/internal/plugincache"
	"github.com/shareed2k/honey/internal/postgres"
	"github.com/shareed2k/honey/internal/queue"
)

// recipeRunnerIface abstracts the execution layer so tests can inject a fake runner
// without spinning up SSH connections.
type recipeRunnerIface interface {
	DryRun(ctx context.Context, req engine.RunRequest) (string, error)
	Execute(ctx context.Context, req engine.RunRequest) (<-chan engine.HostExecResult, error)
	ExecuteAndWait(ctx context.Context, req engine.RunRequest) error
	AssessCommandRisk(ctx context.Context, req engine.RunRequest) []engine.StepRisk
}

// AIAssistant abstracts the AI-related functionality required by recipe generation and assistance.
type AIAssistant interface {
	resolveAssistChatModel(ctx context.Context, requested string) (string, error)
	callDirectLLM(ctx context.Context, endpoint, model, prompt string) (string, error)
	allowAssistRequest(ip string, maxRPM int) bool
}

// RecipesAPI handles all recipe-related HTTP endpoints, isolating them from the main Server.
type RecipesAPI struct {
	opts                  Options
	metrics               *metrics.Registry
	webhookQueue          queue.Queue
	pgPools               *postgres.PoolManager
	ai                    AIAssistant
	plugins               *plugincache.Cache
	sshCache              *engine.ClientCache
	recipeValidationCache *lru.Cache[string, *ValidateContentResponse]
	recipeGraphCache      *lru.Cache[string, *cuetry.RecipeGraphPlan]
	runner                recipeRunnerIface

	webhookDedupCache *ttlcache.Cache[string, string]
	webhookDedupMu    sync.Mutex

	webhookRL      *wrate.TokenBucketLimiter
	webhookCapture *webhookCaptureStore
}

// NewRecipesAPI creates a new isolated router and handler set for Recipes.
func NewRecipesAPI(
	opts Options,
	metrics *metrics.Registry,
	webhookQueue queue.Queue,
	pgPools *postgres.PoolManager,
	ai AIAssistant,
	plugins *plugincache.Cache,
	sshCache *engine.ClientCache,
	valCache *lru.Cache[string, *ValidateContentResponse],
	graphCache *lru.Cache[string, *cuetry.RecipeGraphPlan],
) *RecipesAPI {
	dedupCache := ttlcache.New(
		ttlcache.WithTTL[string, string](24*time.Hour),
		ttlcache.WithDisableTouchOnHit[string, string](),
	)
	go dedupCache.Start()

	api := &RecipesAPI{
		opts:                  opts,
		metrics:               metrics,
		webhookQueue:          webhookQueue,
		pgPools:               pgPools,
		ai:                    ai,
		plugins:               plugins,
		sshCache:              sshCache,
		recipeValidationCache: valCache,
		recipeGraphCache:      graphCache,
		webhookDedupCache:     dedupCache,
		webhookCapture:        newWebhookCaptureStore(),
	}
	rps := opts.WebhookRatePerSecond
	if rps <= 0 {
		rps = 10
	}
	burst := opts.WebhookBurst
	if burst <= 0 {
		burst = 20
	}
	rl, err := wrate.NewTokenBucketLimiter(1024, uint8(burst), rps, time.Second)
	if err != nil {
		panic("webhook rate limiter: " + err.Error())
	}
	api.webhookRL = rl
	var biometric engine.BiometricVerifier
	if opts.WebAuthn != nil {
		biometric = opts.WebAuthn
	}
	api.runner = engine.NewRecipeRunner(engine.RunnerOptions{
		ConfigPath:   opts.ConfigPath,
		Config:       opts.Config,
		ExecRegistry: opts.ExecRegistry,
		Metrics:      metrics,
		Pools:        pgPools,
		Cache:        sshCache,
		RecordDir:    opts.RecordDir,
		Enforcer:     opts.Enforcer,
		Approvals:    opts.Approvals,
		Biometric:    biometric,
	})
	return api
}

// Routes returns a chi.Router with all standard recipe endpoints mounted.
func (api *RecipesAPI) Routes() chi.Router {
	r := chi.NewRouter()

	r.Get("/", api.handleRecipesList)
	r.Get("/library", api.handleRecipesLibrary)
	r.Post("/view", api.handleRecipesView)
	r.Post("/assist", api.handleRecipesAssist)
	r.Post("/assist-fix", api.handleRecipesAssistFix)
	r.Post("/generate", api.handleRecipesGenerate)
	r.Post("/validate-content", api.handleRecipesValidateContent)
	r.Post("/sync-ast", api.handleRecipesSyncAST)
	r.Post("/graph-plan", api.handleRecipesGraphPlan)
	r.Post("/parse", api.handleRecipesParse)
	r.Post("/prompts/upload", api.handleRecipesPromptsUpload)
	r.Post("/prompts/choices", api.handleRecipesPromptsChoices)
	r.Get("/recent-runs", api.handleRecipesRecentRuns)
	r.Get("/schema", api.handleRecipesSchema)
	r.Get("/studio-config", api.handleRecipesStudioConfig)

	r.Route("/store", func(str chi.Router) {
		str.Get("/", api.handleRecipesStoreList)
		str.Post("/git-list", api.handleRecipesStoreGitList)
		str.Post("/git-load", api.handleRecipesStoreGitLoad)
		str.Get("/{name}", api.handleRecipesStoreGet)
		str.Post("/{name}", api.handleRecipesStoreSave)
		str.Delete("/{name}", api.handleRecipesStoreDelete)
	})

	return r
}

// WebhookRoutes returns a chi.Router with unauthenticated webhook entrypoints.
func (api *RecipesAPI) WebhookRoutes() chi.Router {
	r := chi.NewRouter()
	r.Post("/{app_name}/{webhook_name}", api.handleRecipeWebhook)
	return r
}

// WebhookResultRoutes returns a chi.Router with authenticated webhook results endpoints.
func (api *RecipesAPI) WebhookResultRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/{id}", api.handleRecipeWebhookResult)
	return r
}

func (api *RecipesAPI) sshUser(requested string) string {
	user := strings.TrimSpace(requested)
	if user == "" {
		if cfg := api.opts.Config; cfg != nil && cfg.Defaults.SSHUser != "" {
			user = cfg.Defaults.SSHUser
		}
	}
	return user
}

func (api *RecipesAPI) webhookAllow(appName string) bool {
	return api.webhookRL.TakeToken([]byte(appName))
}

// webhookRateLimit enforces the per-app webhook token bucket as HTTP middleware.
// Relies on chi having populated the {app_name} route param before it runs.
func (api *RecipesAPI) webhookRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !api.webhookAllow(chi.URLParam(r, "app_name")) {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (api *RecipesAPI) allowedRecipePathSet() map[string]struct{} {
	out := make(map[string]struct{})
	for _, p := range config.ListDefaultRecipes() {
		if cp, err := filepath.Abs(filepath.Clean(p)); err == nil {
			out[cp] = struct{}{}
		}
	}
	if api.opts.Config != nil {
		for _, app := range api.opts.Config.Apps {
			p := strings.TrimSpace(app.TargetRecipe)
			if p != "" {
				if !filepath.IsAbs(p) && api.opts.ConfigPath != "" {
					p = filepath.Join(filepath.Dir(api.opts.ConfigPath), p)
				}
				if cp, err := filepath.Abs(filepath.Clean(p)); err == nil {
					out[cp] = struct{}{}
				}
			}
		}
	}
	return out
}
