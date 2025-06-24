package logs

import (
	"fmt"
	"io"
	"mcpproxy-go/internal/config"
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// DefaultLogConfig returns default logging configuration
func DefaultLogConfig() *config.LogConfig {
	return &config.LogConfig{
		Level:         "info",
		EnableFile:    true,
		EnableConsole: true,
		Filename:      "mcpproxy.log",
		MaxSize:       10, // 10MB
		MaxBackups:    5,  // 5 backup files
		MaxAge:        30, // 30 days
		Compress:      true,
		JSONFormat:    false, // Use console format for readability
	}
}

// SetupLogger creates a logger with file and console outputs based on configuration
func SetupLogger(config *config.LogConfig) (*zap.Logger, error) {
	if config == nil {
		config = DefaultLogConfig()
	}

	// Parse log level
	var level zapcore.Level
	switch config.Level {
	case "debug":
		level = zap.DebugLevel
	case "info":
		level = zap.InfoLevel
	case "warn":
		level = zap.WarnLevel
	case "error":
		level = zap.ErrorLevel
	default:
		level = zap.InfoLevel
	}

	var cores []zapcore.Core

	// Console output
	if config.EnableConsole {
		consoleEncoder := getConsoleEncoder()
		consoleCore := zapcore.NewCore(
			consoleEncoder,
			zapcore.AddSync(os.Stderr),
			level,
		)
		cores = append(cores, consoleCore)
	}

	// File output
	if config.EnableFile {
		fileCore, err := createFileCore(config, level)
		if err != nil {
			return nil, fmt.Errorf("failed to create file core: %w", err)
		}
		cores = append(cores, fileCore)
	}

	if len(cores) == 0 {
		return nil, fmt.Errorf("no log outputs configured")
	}

	// Combine cores
	core := zapcore.NewTee(cores...)

	// Create logger with caller information
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	return logger, nil
}

// createFileCore creates a file-based logging core
func createFileCore(config *config.LogConfig, level zapcore.Level) (zapcore.Core, error) {
	// Get log file path
	logFilePath, err := GetLogFilePath(config.Filename)
	if err != nil {
		return nil, fmt.Errorf("failed to get log file path: %w", err)
	}

	// Create lumberjack logger for log rotation
	lumberjackLogger := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}

	// Choose encoder based on format preference
	var encoder zapcore.Encoder
	if config.JSONFormat {
		encoder = getJSONEncoder()
	} else {
		encoder = getFileEncoder()
	}

	return zapcore.NewCore(
		encoder,
		zapcore.AddSync(lumberjackLogger),
		level,
	), nil
}

// getConsoleEncoder returns a console-friendly encoder
func getConsoleEncoder() zapcore.Encoder {
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05")
	encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	encoderConfig.EncodeCaller = zapcore.ShortCallerEncoder
	return zapcore.NewConsoleEncoder(encoderConfig)
}

// getFileEncoder returns a file-friendly encoder (structured but readable)
func getFileEncoder() zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02T15:04:05.000Z07:00")
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	encoderConfig.EncodeCaller = zapcore.ShortCallerEncoder
	encoderConfig.ConsoleSeparator = " | "
	return zapcore.NewConsoleEncoder(encoderConfig)
}

// getJSONEncoder returns a JSON encoder for structured logging
func getJSONEncoder() zapcore.Encoder {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.RFC3339)
	encoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder
	encoderConfig.EncodeCaller = zapcore.ShortCallerEncoder
	return zapcore.NewJSONEncoder(encoderConfig)
}

// LoggerInfo represents information about the logger setup
type LoggerInfo struct {
	LogDir        string    `json:"log_dir"`
	LogFile       string    `json:"log_file"`
	Level         string    `json:"level"`
	EnableFile    bool      `json:"enable_file"`
	EnableConsole bool      `json:"enable_console"`
	MaxSize       int       `json:"max_size"`
	MaxBackups    int       `json:"max_backups"`
	MaxAge        int       `json:"max_age"`
	Compress      bool      `json:"compress"`
	JSONFormat    bool      `json:"json_format"`
	CreatedAt     time.Time `json:"created_at"`
}

// GetLoggerInfo returns information about the current logger configuration
func GetLoggerInfo(config *config.LogConfig) (*LoggerInfo, error) {
	if config == nil {
		config = DefaultLogConfig()
	}

	logDir, err := GetLogDir()
	if err != nil {
		return nil, err
	}

	logFile, err := GetLogFilePath(config.Filename)
	if err != nil {
		return nil, err
	}

	return &LoggerInfo{
		LogDir:        logDir,
		LogFile:       logFile,
		Level:         config.Level,
		EnableFile:    config.EnableFile,
		EnableConsole: config.EnableConsole,
		MaxSize:       config.MaxSize,
		MaxBackups:    config.MaxBackups,
		MaxAge:        config.MaxAge,
		Compress:      config.Compress,
		JSONFormat:    config.JSONFormat,
		CreatedAt:     time.Now(),
	}, nil
}

// CreateTestWriter creates a writer for testing that captures both file and memory output
func CreateTestWriter() (io.Writer, *os.File, error) {
	// Create a temporary file for testing
	tmpFile, err := os.CreateTemp("", "mcpproxy-test-*.log")
	if err != nil {
		return nil, nil, err
	}

	// Return a multi-writer that writes to both the file and can be read back
	return tmpFile, tmpFile, nil
}

// CleanupTestWriter removes temporary test files
func CleanupTestWriter(file *os.File) error {
	if file != nil {
		filename := file.Name()
		file.Close()
		return os.Remove(filename)
	}
	return nil
}
