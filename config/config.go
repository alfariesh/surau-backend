package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
)

type (
	// Config -.
	Config struct {
		App     app
		HTTP    http
		Log     log
		PG      pg
		JWT     jwt
		RAG     rag
		Metrics metrics
		Swagger swagger
	}

	// App -.
	app struct {
		Name    string `env:"APP_NAME,required"`
		Version string `env:"APP_VERSION,required"`
	}

	// HTTP -.
	http struct {
		Port           string `env:"HTTP_PORT,required"`
		UsePreforkMode bool   `env:"HTTP_USE_PREFORK_MODE" envDefault:"false"`
	}

	// Log -.
	log struct {
		Level string `env:"LOG_LEVEL,required"`
	}

	// PG -.
	pg struct {
		PoolMax int    `env:"PG_POOL_MAX,required"`
		URL     string `env:"PG_URL,required"`
	}

	// JWT -.
	jwt struct {
		Secret      string        `env:"JWT_SECRET,required"`
		TokenExpiry time.Duration `env:"JWT_TOKEN_EXPIRY" envDefault:"24h"`
	}

	// RAG -.
	rag struct {
		LLMBaseURL      string        `env:"RAG_LLM_BASE_URL" envDefault:"https://ai.sumopod.com/v1"`
		LLMAPIKey       string        `env:"RAG_LLM_API_KEY"`
		LLMModel        string        `env:"RAG_LLM_MODEL" envDefault:"glm-5.1"`
		LLMTimeout      time.Duration `env:"RAG_LLM_TIMEOUT" envDefault:"45s"`
		LLMMaxTokens    int           `env:"RAG_LLM_MAX_TOKENS" envDefault:"1400"`
		LLMTemperature  float64       `env:"RAG_LLM_TEMPERATURE" envDefault:"0.1"`
		MaxContextPages int           `env:"RAG_MAX_CONTEXT_PAGES" envDefault:"8"`
	}

	// Metrics -.
	metrics struct {
		Enabled bool `env:"METRICS_ENABLED" envDefault:"true"`
	}

	// Swagger -.
	swagger struct {
		Enabled bool `env:"SWAGGER_ENABLED" envDefault:"false"`
	}
)

// NewConfig returns app config.
func NewConfig() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}
	if cfg.PG.PoolMax < 1 || cfg.PG.PoolMax > 100 {
		return nil, fmt.Errorf("config error: PG_POOL_MAX must be between 1 and 100")
	}
	if cfg.RAG.LLMTimeout <= 0 {
		return nil, fmt.Errorf("config error: RAG_LLM_TIMEOUT must be positive")
	}
	if cfg.RAG.LLMMaxTokens < 1 {
		return nil, fmt.Errorf("config error: RAG_LLM_MAX_TOKENS must be positive")
	}
	if cfg.RAG.MaxContextPages < 1 {
		return nil, fmt.Errorf("config error: RAG_MAX_CONTEXT_PAGES must be positive")
	}

	return cfg, nil
}
