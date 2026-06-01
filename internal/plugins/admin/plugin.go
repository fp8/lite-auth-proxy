package admin

import (
	"net/http"

	"github.com/fp8/lite-auth-proxy/internal/admin"
	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/auth/jwt"
	"github.com/fp8/lite-auth-proxy/internal/plugin"
	"github.com/fp8/lite-auth-proxy/internal/proxy"
)

func init() {
	plugin.Register(&adminPlugin{})
}

type adminPlugin struct{}

func (p *adminPlugin) Name() string  { return "admin" }
func (p *adminPlugin) Priority() int { return 50 }

func (p *adminPlugin) ValidateConfig(cfg *config.Config) error {
	return nil
}

func (p *adminPlugin) RegisterRoutes(mux *http.ServeMux, deps *plugin.Deps) error {
	cfg := deps.Config
	if !cfg.Admin.Enabled {
		return nil
	}

	adminValidator := jwt.NewValidator(&cfg.Admin.JWT)
	adminAuth := admin.AdminAuthMiddleware(adminValidator, cfg.Admin.JWT.AllowedEmails, cfg.Admin.JWT.Filters)
	mux.Handle("POST /admin/control", adminAuth(admin.ControlHandler(deps.RuleStore, deps.RateLimiters)))
	mux.Handle("GET /admin/status", adminAuth(admin.StatusHandler(deps.RuleStore, deps.RateLimiters)))
	return nil
}

func (p *adminPlugin) BuildMiddleware(deps *plugin.Deps) ([]plugin.Middleware, error) {
	cfg := deps.Config
	if !cfg.Admin.Enabled {
		return nil, nil
	}

	return []plugin.Middleware{
		proxy.DynamicRuleCheck(deps.RuleStore),
	}, nil
}
