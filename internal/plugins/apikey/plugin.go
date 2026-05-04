package apikey

import (
	"net/http"

	"github.com/fp8/lite-auth-proxy/internal/auth/apikey"
	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/plugin"
)

func init() {
	plugin.Register(&apiKeyPlugin{})
}

type apiKeyPlugin struct{}

func (p *apiKeyPlugin) Name() string  { return "apikey" }
func (p *apiKeyPlugin) Priority() int { return 90 }

func (p *apiKeyPlugin) ValidateConfig(cfg *config.Config) error {
	return nil
}

func (p *apiKeyPlugin) Authenticate(r *http.Request, cfg *config.AuthConfig) (map[string]string, error) {
	return apikey.ValidateAPIKey(r, cfg)
}
