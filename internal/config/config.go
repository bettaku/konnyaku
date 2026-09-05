package config

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	Address, DatabaseURL, PublicURL, RepositoryRoot string
	GitHubToken, WebhookSecret                      string
	OpenAIBaseURL, OpenAIKey, OpenAIModel           string
	GoogleProject, GoogleLocation                   string
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func Load() (Config, error) {
	c := Config{Address: env("LISTEN_ADDR", "127.0.0.1:8080"), DatabaseURL: os.Getenv("DATABASE_URL"), PublicURL: env("PUBLIC_URL", "http://localhost:8080"), RepositoryRoot: env("REPOSITORY_ROOT", "./data/repositories"), GitHubToken: os.Getenv("GITHUB_TOKEN"), WebhookSecret: os.Getenv("GITHUB_WEBHOOK_SECRET"), OpenAIBaseURL: env("OPENAI_BASE_URL", "https://api.openai.com/v1"), OpenAIKey: os.Getenv("OPENAI_API_KEY"), OpenAIModel: os.Getenv("OPENAI_MODEL"), GoogleProject: os.Getenv("GOOGLE_CLOUD_PROJECT"), GoogleLocation: env("GOOGLE_CLOUD_LOCATION", "global")}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return c, errors.New("PUBLIC_URL must be an http(s) origin without a path")
	}
	c.PublicURL = strings.TrimSuffix(c.PublicURL, "/")
	if c.WebhookSecret != "" && len(c.WebhookSecret) < 32 {
		return c, errors.New("GITHUB_WEBHOOK_SECRET must contain at least 32 characters")
	}
	return c, nil
}
