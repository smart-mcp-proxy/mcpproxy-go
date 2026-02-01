package patterns

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test MySQL connection string detection
func TestMySQLConnectionStringPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "MySQL URI format",
			input:     "mysql://user:password@localhost:3306/database",
			wantMatch: true,
		},
		{
			name:      "MySQL with special chars in password",
			input:     "mysql://admin:p@ssw0rd!@db.example.com:3306/mydb",
			wantMatch: true,
		},
		{
			name:      "MySQL DSN format",
			input:     "user:password@tcp(localhost:3306)/database",
			wantMatch: true,
		},
		{
			name:      "MySQL without password",
			input:     "mysql://user@localhost/database",
			wantMatch: false, // No credential exposure
		},
	}

	patterns := GetDatabasePatterns()
	mysqlPattern := findPatternByName(patterns, "mysql_connection")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if mysqlPattern == nil {
				t.Skip("MySQL connection pattern not implemented yet")
				return
			}
			matches := mysqlPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test PostgreSQL connection string detection
func TestPostgresConnectionStringPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "PostgreSQL URI format",
			input:     "postgresql://user:password@localhost:5432/database",
			wantMatch: true,
		},
		{
			name:      "Postgres short form",
			input:     "postgres://admin:secret@db.example.com/mydb",
			wantMatch: true,
		},
		{
			name:      "PostgreSQL with options",
			input:     "postgresql://user:pass@localhost/db?sslmode=require",
			wantMatch: true,
		},
		{
			name:      "PostgreSQL without password",
			input:     "postgresql://user@localhost/database",
			wantMatch: false, // No credential exposure
		},
	}

	patterns := GetDatabasePatterns()
	pgPattern := findPatternByName(patterns, "postgres_connection")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if pgPattern == nil {
				t.Skip("PostgreSQL connection pattern not implemented yet")
				return
			}
			matches := pgPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test MongoDB connection string detection
func TestMongoDBConnectionStringPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "MongoDB standard URI",
			input:     "mongodb://user:password@localhost:27017/database",
			wantMatch: true,
		},
		{
			name:      "MongoDB Atlas SRV",
			input:     "mongodb+srv://admin:secret@cluster0.xxxxx.mongodb.net/mydb",
			wantMatch: true,
		},
		{
			name:      "MongoDB with replica set",
			input:     "mongodb://user:pass@host1:27017,host2:27017/db?replicaSet=rs0",
			wantMatch: true,
		},
		{
			name:      "MongoDB without credentials",
			input:     "mongodb://localhost:27017/database",
			wantMatch: false, // No credential exposure
		},
	}

	patterns := GetDatabasePatterns()
	mongoPattern := findPatternByName(patterns, "mongodb_connection")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if mongoPattern == nil {
				t.Skip("MongoDB connection pattern not implemented yet")
				return
			}
			matches := mongoPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test Redis connection string detection
func TestRedisConnectionStringPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "Redis URI with password",
			input:     "redis://:password@localhost:6379/0",
			wantMatch: true,
		},
		{
			name:      "Redis URI with user and password",
			input:     "redis://default:mypassword@redis.example.com:6379",
			wantMatch: true,
		},
		{
			name:      "Redis Sentinel",
			input:     "redis-sentinel://:password@sentinel1:26379,sentinel2:26379/mymaster",
			wantMatch: true,
		},
		{
			name:      "Redis without password",
			input:     "redis://localhost:6379",
			wantMatch: false, // No credential exposure
		},
	}

	patterns := GetDatabasePatterns()
	redisPattern := findPatternByName(patterns, "redis_connection")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if redisPattern == nil {
				t.Skip("Redis connection pattern not implemented yet")
				return
			}
			matches := redisPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}

// Test generic database password detection
func TestDatabasePasswordPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMatch bool
	}{
		{
			name:      "DB_PASSWORD env var",
			input:     "DB_PASSWORD=mysecretpassword123",
			wantMatch: true,
		},
		{
			name:      "DATABASE_PASSWORD env var",
			input:     "DATABASE_PASSWORD=secret",
			wantMatch: true,
		},
		{
			name:      "password in JSON config",
			input:     `"db_password": "mysecret"`,
			wantMatch: true,
		},
		{
			name:      "MYSQL_ROOT_PASSWORD",
			input:     "MYSQL_ROOT_PASSWORD=rootsecret",
			wantMatch: true,
		},
		{
			name:      "POSTGRES_PASSWORD",
			input:     "POSTGRES_PASSWORD=pgpassword",
			wantMatch: true,
		},
		{
			name:      "empty password",
			input:     "DB_PASSWORD=",
			wantMatch: false,
		},
	}

	patterns := GetDatabasePatterns()
	dbPassPattern := findPatternByName(patterns, "database_password")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if dbPassPattern == nil {
				t.Skip("Database password pattern not implemented yet")
				return
			}
			matches := dbPassPattern.Match(tt.input)
			if tt.wantMatch {
				assert.NotEmpty(t, matches, "expected match for: %s", tt.input)
			} else {
				assert.Empty(t, matches, "expected no match for: %s", tt.input)
			}
		})
	}
}
