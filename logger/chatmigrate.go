package logger

import (
	"encoding/json"
	"log"
	"strconv"
	"sync"
)

var OnChatMigrated func(oldID, newID string) error

func MigratedChatID(body []byte) string {
	var p struct {
		Parameters struct {
			MigrateToChatID int64 `json:"migrate_to_chat_id"`
		} `json:"parameters"`
	}
	if json.Unmarshal(body, &p) != nil || p.Parameters.MigrateToChatID == 0 {
		return ""
	}
	return strconv.FormatInt(p.Parameters.MigrateToChatID, 10)
}

var (
	migratedChatsMu sync.RWMutex
	migratedChats   = map[string]string{}
)

func resolveMigratedChat(id string) string {
	migratedChatsMu.RLock()
	defer migratedChatsMu.RUnlock()
	if n, ok := migratedChats[id]; ok {
		return n
	}
	return id
}

func ApplyChatMigration(oldID, newID string) {
	migratedChatsMu.Lock()
	migratedChats[oldID] = newID
	migratedChatsMu.Unlock()
	if OnChatMigrated == nil {
		log.Printf("logger: Telegram chat %s was upgraded to a supergroup (new id %s) but nothing saves it here, update the alert chat id setting", oldID, newID)
		return
	}
	if err := OnChatMigrated(oldID, newID); err != nil {
		log.Printf("logger: Telegram chat %s was upgraded to %s but saving the new id failed: %v", oldID, newID, err)
		return
	}
	log.Printf("logger: Telegram chat %s was upgraded to a supergroup, alert chat id replaced with %s", oldID, newID)
}

func SetOnChatMigrated(fn func(oldID, newID string) error) { OnChatMigrated = fn }
