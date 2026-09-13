package config

import (
	"github.com/caarlos0/env/v11"
)

// Config 应用配置，全部通过环境变量注入。
type Config struct {
	ServerPort     string `env:"SERVER_PORT" envDefault:"8080"`
	RunMode        string `env:"RUN_MODE" envDefault:"release"`
	LogLevel       string `env:"LOG_LEVEL" envDefault:"info"`
	DBHost         string `env:"DB_HOST" envDefault:"127.0.0.1"`
	DBPort         string `env:"DB_PORT" envDefault:"5432"`
	DBDriver       string `env:"DB_DRIVER" envDefault:"postgres"` // postgres（部署默认）/ sqlite（仅本地无数据库环境）
	DBName         string `env:"DB_NAME" envDefault:"communitygarden_db"`
	DBUser         string `env:"DB_USER" envDefault:"communitygarden_user"`
	DBPassword     string `env:"DB_PASSWORD" envDefault:"communitygarden_pwd"`
	DBSSLMode      string `env:"DB_SSLMODE" envDefault:"disable"`
	// 数据库连接池：有界化后，突发请求在客户端排队，避免打爆 PostgreSQL（max_connections）。
	DBMaxOpenConns  int `env:"DB_MAX_OPEN_CONNS" envDefault:"25"`
	DBMaxIdleConns  int `env:"DB_MAX_IDLE_CONNS" envDefault:"10"`
	DBConnMaxLifeS  int `env:"DB_CONN_MAX_LIFETIME_SECONDS" envDefault:"1800"`
	// 等待空闲连接的超时（连接池排队上限），超时后按资源繁忙重试/返回 503，而非 500。
	DBAcquireTimeoutMS int `env:"DB_ACQUIRE_TIMEOUT_MS" envDefault:"3000"`
	// PostgreSQL 服务端语句/锁等待超时（毫秒）。
	DBStatementTimeoutMS int `env:"DB_STATEMENT_TIMEOUT_MS" envDefault:"5000"`
	DBLockTimeoutMS      int `env:"DB_LOCK_TIMEOUT_MS" envDefault:"3000"`
	RedisHost      string `env:"REDIS_HOST" envDefault:"127.0.0.1"`
	RedisPort      string `env:"REDIS_PORT" envDefault:"6379"`
	RedisPassword  string `env:"REDIS_PASSWORD" envDefault:""`
	RedisDB        int    `env:"REDIS_DB" envDefault:"0"`
	JWTSecret      string `env:"JWT_SECRET" envDefault:"change_me_to_a_long_random_string"`
	JWTExpireHours int    `env:"JWT_EXPIRE_HOURS" envDefault:"72"`
	// 限流：Redis 不可用时使用进程内固定窗口兜底（0 表示禁用兜底）。
	RateLimitPerMin int `env:"RATE_LIMIT_PER_MIN" envDefault:"300"`
}

// Load 从环境变量加载配置。
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
