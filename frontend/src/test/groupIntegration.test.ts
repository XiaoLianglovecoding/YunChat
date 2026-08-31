import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Group, GroupMember } from "../../goim-api-types";
import { refreshGroupConversations } from "../components/realtime/RealtimeBootstrap";
import { canManageGroupProfile } from "../features/groups/GroupManagement";
import { groupsApi } from "../lib/api";
import { useChatStore } from "../stores/chatStore";

const group: Group = {
  id: 22,
  name: "项目群",
  notice: "",
  owner_id: 1,
  max_members: 500,
  created_at: "2026-08-31T12:00:00Z",
  updated_at: "2026-08-31T12:00:00Z",
};

describe("GROUP-001 frontend integration", () => {
  beforeEach(() => {
    useChatStore.setState({
      mode: null,
      liveUserId: null,
      connectionState: "idle",
      syncCompleted: false,
      conversations: [],
      messagesByConversation: {},
      lastSyncTime: 0,
      lastSyncMsgId: 0,
    });
  });

  it("lets the owner manage the profile before the member-list endpoint is available", () => {
    expect(canManageGroupProfile(group, undefined, 1)).toBe(true);
    expect(canManageGroupProfile(group, undefined, 2)).toBe(false);

    const admin = { role: 1 } as GroupMember;
    expect(canManageGroupProfile(group, admin, 2)).toBe(true);
  });

  it("hydrates group conversations directly from the authenticated user's group list", async () => {
    useChatStore.getState().initializeLive(1);
    const list = vi.spyOn(groupsApi, "list").mockResolvedValue([
      group,
      { ...group, id: 23, name: "学习群", owner_id: 2 },
    ]);

    await refreshGroupConversations(1);

    expect(list).toHaveBeenCalledOnce();
    expect(useChatStore.getState().conversations).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: "g_22", targetId: 22, name: "项目群", group: true }),
      expect.objectContaining({ id: "g_23", targetId: 23, name: "学习群", group: true }),
    ]));
  });

  it("does not write a late group-list response into a different user's session", async () => {
    useChatStore.getState().initializeLive(1);
    let resolveList!: (groups: Group[]) => void;
    vi.spyOn(groupsApi, "list").mockReturnValue(new Promise((resolve) => { resolveList = resolve; }));

    const refresh = refreshGroupConversations(1);
    useChatStore.getState().initializeLive(2);
    resolveList([group]);
    await refresh;

    expect(useChatStore.getState().liveUserId).toBe(2);
    expect(useChatStore.getState().conversations).toEqual([]);
  });
});
