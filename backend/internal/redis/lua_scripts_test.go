package redis

import (
	"strings"
	"testing"
)

func TestLuaCatalogHasStableUniqueHashes(t *testing.T) {
	hashes := LuaScriptHashes()
	if len(hashes) != 6 {
		t.Fatalf("script count = %d, want 6", len(hashes))
	}
	seen := map[string]string{}
	for name, hash := range hashes {
		if len(hash) != 40 {
			t.Errorf("%s SHA1 length = %d", name, len(hash))
		}
		if previous, ok := seen[hash]; ok {
			t.Errorf("scripts %s and %s unexpectedly have same SHA", previous, name)
		}
		seen[hash] = name
	}
}

func TestPrivateMessageLuaChecksBlacklistInBothDirections(t *testing.T) {
	want := []string{
		"'blacklist:' .. receiverID, senderID",
		"'blacklist:' .. senderID, receiverID",
		"blockedByReceiver == 1 or blockedBySender == 1",
	}
	for _, snippet := range want {
		if !strings.Contains(luaPrivateMsgCheck, snippet) {
			t.Errorf("private message Lua is missing %q", snippet)
		}
	}
}

func TestGroupMessageLuaEvaluatesMuteDeadlineAgainstRedisTime(t *testing.T) {
	want := []string{
		"redis.call('TIME')",
		"tonumber(info.muted_until)",
		"mutedUntil > milliseconds",
	}
	for _, snippet := range want {
		if !strings.Contains(luaGroupMsgCheck, snippet) {
			t.Errorf("group message Lua is missing %q", snippet)
		}
	}
	if strings.Contains(luaGroupMsgCheck, "if info.muted then") {
		t.Error("group message Lua must not rely on a frozen muted boolean")
	}
	if !strings.Contains(luaGroupMsgCheck, "if not memberInfo then") {
		t.Error("group message Lua must fail closed when member metadata is missing")
	}
}
