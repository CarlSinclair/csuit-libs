package redis

import (
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/CarlSinclair/csuit-libs/logger"
)

func ProcessingName(queueName string) string { return queueName + ":processing" }

func DequeueReliable(queueName string, timeout time.Duration) ([]byte, bool, error) {
	if err := requireRedis("DequeueReliable", queueName); err != nil {
		return nil, false, err
	}
	v, err := Client().BLMove(Ctx, Key(queueName), Key(ProcessingName(queueName)), "RIGHT", "LEFT", timeout).Result()
	if err != nil {
		if isNil(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return []byte(v), true, nil
}

func Ack(queueName string, payload []byte) error {
	if err := requireRedis("Ack", queueName); err != nil {
		return err
	}
	n, err := Client().LRem(Ctx, Key(ProcessingName(queueName)), 1, payload).Result()
	if err != nil {
		logger.Error("Redis.Ack: LREM failed queue=%s err=%v", queueName, err)
		return err
	}
	if n == 0 {
		logger.Warn("Redis.Ack: item not found in processing list queue=%s", queueName)
		return errors.New("item not in processing list")
	}
	return nil
}

func RecoverProcessing(queueName string) (int, error) {
	if err := requireRedis("RecoverProcessing", queueName); err != nil {
		return 0, err
	}
	moved := 0
	for {
		_, err := Client().LMove(Ctx, Key(ProcessingName(queueName)), Key(queueName), "RIGHT", "RIGHT").Result()
		if err != nil {
			if isNil(err) {
				return moved, nil
			}
			logger.Error("Redis.RecoverProcessing: LMOVE failed queue=%s moved=%d err=%v", queueName, moved, err)
			return moved, err
		}
		moved++
	}
}

func isNil(err error) bool { return errors.Is(err, goredis.Nil) }
