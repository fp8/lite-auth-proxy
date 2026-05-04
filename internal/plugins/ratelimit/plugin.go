package ratelimit

import (
	"time"

	"github.com/fp8/lite-auth-proxy/internal/config"
	"github.com/fp8/lite-auth-proxy/internal/plugin"
	"github.com/fp8/lite-auth-proxy/internal/proxy"
	"github.com/fp8/lite-auth-proxy/internal/ratelimit"
)

func init() {
	plugin.Register(&rateLimitPlugin{})
}

type rateLimitPlugin struct{}

func (p *rateLimitPlugin) Name() string  { return "ratelimit" }
func (p *rateLimitPlugin) Priority() int { return 60 }

func (p *rateLimitPlugin) ValidateConfig(cfg *config.Config) error {
	return nil
}

func (p *rateLimitPlugin) BuildMiddleware(deps *plugin.Deps) ([]plugin.Middleware, error) {
	cfg := deps.Config

	// Create rate limiters — one per traffic type.
	ipLimiter := ratelimit.NewRateLimiter(ratelimit.RateLimiterConfig{
		Name:           "ip",
		Enabled:        cfg.Security.RateLimit.Enabled,
		RequestsPerMin: cfg.Security.RateLimit.RequestsPerMin,
		BanDuration:    time.Duration(cfg.Security.RateLimit.BanForMin) * time.Minute,
		ThrottleDelay:  time.Duration(cfg.Security.RateLimit.ThrottleDelayMs) * time.Millisecond,
		MaxDelaySlots:  cfg.Security.RateLimit.MaxDelaySlots,
	})

	apikeyLimiter := ratelimit.NewRateLimiter(ratelimit.RateLimiterConfig{
		Name:           "apikey",
		Enabled:        cfg.Security.ApiKeyRateLimit.Enabled,
		RequestsPerMin: cfg.Security.ApiKeyRateLimit.RequestsPerMin,
		BanDuration:    time.Duration(cfg.Security.ApiKeyRateLimit.BanForMin) * time.Minute,
		ThrottleDelay:  time.Duration(cfg.Security.ApiKeyRateLimit.ThrottleDelayMs) * time.Millisecond,
		MaxDelaySlots:  cfg.Security.ApiKeyRateLimit.MaxDelaySlots,
	})

	jwtLimiter := ratelimit.NewRateLimiter(ratelimit.RateLimiterConfig{
		Name:           "jwt",
		Enabled:        cfg.Security.JwtRateLimit.Enabled,
		RequestsPerMin: cfg.Security.JwtRateLimit.RequestsPerMin,
		BanDuration:    time.Duration(cfg.Security.JwtRateLimit.BanForMin) * time.Minute,
		ThrottleDelay:  time.Duration(cfg.Security.JwtRateLimit.ThrottleDelayMs) * time.Millisecond,
		MaxDelaySlots:  cfg.Security.JwtRateLimit.MaxDelaySlots,
	})

	// Build request matcher for API key rate limiting.
	matchRules := make([]ratelimit.RequestMatchRule, len(cfg.Security.ApiKeyRateLimit.Match))
	for i, m := range cfg.Security.ApiKeyRateLimit.Match {
		matchRules[i] = ratelimit.RequestMatchRule{
			Host:   m.Host,
			Path:   m.Path,
			Header: m.Header,
		}
	}
	apiKeyMatcher, err := ratelimit.NewRequestMatcher(matchRules)
	if err != nil {
		return nil, err
	}

	// Store limiters in deps for other plugins (admin, startup loader).
	deps.RateLimiters["ip"] = ipLimiter
	deps.RateLimiters["apikey"] = apikeyLimiter
	deps.RateLimiters["jwt"] = jwtLimiter

	skipIfJwt := cfg.Security.RateLimit.SkipIfJwtIdentified == nil || *cfg.Security.RateLimit.SkipIfJwtIdentified

	middlewares := []plugin.Middleware{
		proxy.ApiKeyRateLimit(apikeyLimiter, apiKeyMatcher, cfg.Security.ApiKeyRateLimit.KeyHeader, cfg.Security.ApiKeyRateLimit.IncludeIP),
		proxy.JwtRateLimit(jwtLimiter, cfg.Security.JwtRateLimit.IncludeIP),
		proxy.IpRateLimit(ipLimiter, skipIfJwt),
	}

	return middlewares, nil
}
