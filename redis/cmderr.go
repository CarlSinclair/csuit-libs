package redis

import "github.com/CarlSinclair/csuit-libs/logger"

func LogErr(what string, cmd interface{ Err() error }) {
	if err := cmd.Err(); err != nil {
		logger.Warn("redis %s failed: %v", what, err)
	}
}
