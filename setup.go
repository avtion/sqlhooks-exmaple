package example

import (
	"log"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func setupZapLogging(level zapcore.Level) *zap.Logger {
	config := zap.NewDevelopmentConfig()
	config.Level = zap.NewAtomicLevelAt(level)

	logger, err := config.Build()
	if err != nil {
		log.Fatal(err)
	}

	zap.ReplaceGlobals(logger)

	if _, err = zap.RedirectStdLogAt(logger, zap.InfoLevel); err != nil {
		log.Fatal(err)
	}
	return logger
}
