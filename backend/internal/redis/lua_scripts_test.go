package redis

import "testing"

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
