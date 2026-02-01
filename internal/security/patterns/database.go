package patterns

// GetDatabasePatterns returns all database credential detection patterns
func GetDatabasePatterns() []*Pattern {
	return []*Pattern{
		mysqlConnectionPattern(),
		postgresConnectionPattern(),
		mongodbConnectionPattern(),
		redisConnectionPattern(),
		databasePasswordPattern(),
	}
}

// MySQL connection string with credentials
func mysqlConnectionPattern() *Pattern {
	// Matches mysql:// with user:password or DSN format
	return NewPattern("mysql_connection").
		WithRegex(`(?:mysql://[^:]+:[^@]+@[^/]+|[a-zA-Z0-9_]+:[^@]+@tcp\([^)]+\))`).
		WithCategory(CategoryDatabaseCred).
		WithSeverity(SeverityCritical).
		WithDescription("MySQL connection string with credentials").
		Build()
}

// PostgreSQL connection string with credentials
func postgresConnectionPattern() *Pattern {
	// Matches postgresql:// or postgres:// with user:password
	return NewPattern("postgres_connection").
		WithRegex(`postgres(?:ql)?://[^:]+:[^@]+@[^\s]+`).
		WithCategory(CategoryDatabaseCred).
		WithSeverity(SeverityCritical).
		WithDescription("PostgreSQL connection string with credentials").
		Build()
}

// MongoDB connection string with credentials
func mongodbConnectionPattern() *Pattern {
	// Matches mongodb:// or mongodb+srv:// with user:password
	return NewPattern("mongodb_connection").
		WithRegex(`mongodb(?:\+srv)?://[^:]+:[^@]+@[^\s]+`).
		WithCategory(CategoryDatabaseCred).
		WithSeverity(SeverityCritical).
		WithDescription("MongoDB connection string with credentials").
		Build()
}

// Redis connection string with credentials
func redisConnectionPattern() *Pattern {
	// Matches redis:// or redis-sentinel:// with password
	return NewPattern("redis_connection").
		WithRegex(`redis(?:-sentinel)?://[^@]*:[^@]+@[^\s]+`).
		WithCategory(CategoryDatabaseCred).
		WithSeverity(SeverityHigh).
		WithDescription("Redis connection string with credentials").
		Build()
}

// Generic database password pattern
func databasePasswordPattern() *Pattern {
	// Matches common database password environment variables and config keys
	// Handles both env var format (KEY=value) and JSON format ("key": "value")
	return NewPattern("database_password").
		WithRegex(`(?i)["']?(?:DB_PASSWORD|DATABASE_PASSWORD|MYSQL_(?:ROOT_)?PASSWORD|POSTGRES_PASSWORD|MONGO(?:DB)?_PASSWORD|REDIS_PASSWORD|db_password|database_password)["']?\s*[=:]\s*["']?[^"'\s]+["']?`).
		WithCategory(CategoryDatabaseCred).
		WithSeverity(SeverityHigh).
		WithDescription("Database password in configuration").
		WithValidator(func(match string) bool {
			// Ensure password is not empty
			return len(match) > 15 // At least has the key and some value
		}).
		Build()
}
