package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
)

// LuaScriptHashes 可记录到启动日志，用来确认当前实例实际加载的脚本版本。
func LuaScriptHashes() map[string]string {
	result := make(map[string]string, len(allScripts))
	for name, script := range allScripts {
		result[name] = script.Hash()
	}
	return result
}

var allScripts = map[string]*goredis.Script{
	"private_msg_check": goredis.NewScript(luaPrivateMsgCheck),
	"group_msg_check":   goredis.NewScript(luaGroupMsgCheck),
	"inbox_mark_read":   goredis.NewScript(luaInboxMarkRead),
	"revoke_msg":        goredis.NewScript(luaRevokeMsg),
	"moment_like":       goredis.NewScript(luaMomentLike),
	"moment_unlike":     goredis.NewScript(luaMomentUnlike),
}

func LoadLuaScripts(rdb *goredis.Client, ctx context.Context) error {
	for name, script := range allScripts {
		loaded, err := script.Load(ctx, rdb).Result()
		if err != nil {
			return fmt.Errorf("load lua script %s (%s): %w", name, script.Hash(), err)
		}
		if loaded != script.Hash() {
			return fmt.Errorf("load lua script %s: expected sha %s, got %s", name, script.Hash(), loaded)
		}
	}
	return nil
}

func runScript(name string, rdb *goredis.Client, ctx context.Context, keys []string, args ...any) *goredis.Cmd {
	return allScripts[name].Run(ctx, rdb, keys, args...)
}
