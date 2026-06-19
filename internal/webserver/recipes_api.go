package webserver

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/postgres"
	"github.com/shareed2k/honey/internal/queue"
)

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
	recipeValidationCache *lru.Cache[string, *ValidateContentResponse]
	recipeGraphCache      *lru.Cache[string, *cuetry.RecipeGraphPlan]
}

// NewRecipesAPI creates a new isolated router and handler set for Recipes.
func NewRecipesAPI(
	opts Options,
	metrics *metrics.Registry,
	webhookQueue queue.Queue,
	pgPools *postgres.PoolManager,
	ai AIAssistant,
	valCache *lru.Cache[string, *ValidateContentResponse],
	graphCache *lru.Cache[string, *cuetry.RecipeGraphPlan],
) *RecipesAPI {
	return &RecipesAPI{
		opts:                  opts,
		metrics:               metrics,
		webhookQueue:          webhookQueue,
		pgPools:               pgPools,
		ai:                    ai,
		recipeValidationCache: valCache,
		recipeGraphCache:      graphCache,
	}
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
