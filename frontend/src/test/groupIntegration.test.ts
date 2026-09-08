import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import type { Group, GroupMember } from "../../goim-api-types";
import { handleConnectionState, handleServerMessage, refreshGroupConversations } from "../components/realtime/RealtimeBootstrap";
import { canManageGroupProfile } from "../features/groups/GroupManagement";
import { groupsApi } from "../lib/api";
import { queryClient } from "../lib/queryClient";
import { goimSocket } from "../realtime/socket";
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

describe("group frontend integration", () => {
  afterEach(() => vi.restoreAllMocks());

  beforeEach(() => {
    queryClient.clear();
    useChatStore.setState({
      mode: null,
      liveUserId: null,
      connectionState: "idle",
      syncCompleted: false,
      conversations: [],
      messagesByConversation: {},
      dissolvedGroupIds: [],
      lastSyncTime: 0,
      lastSyncMsgId: 0,
    });
  });

  it("lets the owner manage the profile before the member-list endpoint is available", () => {
    expect(canManageGroupProfile(group, undefined, 1)).toBe(true);
    expect(canManageGroupProfile(group, undefined, 2)).toBe(false);

    const admin = { role: 1 } as GroupMember;
    expect(canManageGroupProfile(group, admin, 2)).toBe(true);

    const staleSecondOwner = { role: 2 } as GroupMember;
    expect(canManageGroupProfile(group, staleSecondOwner, 2)).toBe(false);
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

  it("invalidates group truth when ownership or membership changes over WebSocket", () => {
    const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

    handleServerMessage({
      type: "groupUpdated",
      data: { groupId: 22, reason: "owner_transferred" },
    }, 1, () => undefined);

    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["group", 22] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["group-members", 22] });
  });

  it("removes the conversation and stale group queries after a leave notification", () => {
    useChatStore.getState().initializeLive(1);
    useChatStore.getState().addGroupConversation(22, group.name);
    queryClient.setQueryData(["group", 22], group);
    queryClient.setQueryData(["group-members", 22], { items: [], total: 0 });
    vi.spyOn(groupsApi, "list").mockResolvedValue([]);

    handleServerMessage({
      type: "groupRemoved",
      data: { groupId: 22, reason: "left" },
    }, 1, () => undefined);

    expect(useChatStore.getState().conversations).toHaveLength(0);
    expect(queryClient.getQueryData(["group", 22])).toBeUndefined();
    expect(queryClient.getQueryData(["group-members", 22])).toBeUndefined();
  });

  it("removes local messages and group queries for every member after dissolution", () => {
    useChatStore.getState().initializeLive(1);
    useChatStore.getState().addGroupConversation(22, group.name);
    queryClient.setQueryData(["group", 22], group);
    queryClient.setQueryData(["group-members", 22], { items: [], total: 0 });
    vi.spyOn(groupsApi, "list").mockResolvedValue([]);

    handleServerMessage({
      type: "groupRemoved",
      data: { groupId: 22, reason: "dissolved" },
    }, 1, () => undefined);

    expect(useChatStore.getState().conversations).toHaveLength(0);
    expect(useChatStore.getState().messagesByConversation.g_22).toBeUndefined();
    expect(queryClient.getQueryData(["group", 22])).toBeUndefined();
    expect(queryClient.getQueryData(["group-members", 22])).toBeUndefined();
  });

  it("acknowledges but never revives a dissolved group from a late message", () => {
    useChatStore.getState().initializeLive(1);
    useChatStore.getState().addGroupConversation(22, group.name);
    vi.spyOn(groupsApi, "list").mockResolvedValue([]);
    const getGroup = vi.spyOn(groupsApi, "get");
    const send = vi.spyOn(goimSocket, "send").mockReturnValue(true);

    handleServerMessage({
      type: "groupRemoved",
      data: { groupId: 22, reason: "dissolved" },
    }, 1, () => undefined);
    handleServerMessage({
      type: "msg",
      data: { msgId: 9001, convId: "g_22", convType: 2, fromId: 2, toId: 22, msgType: 1, content: "解散前在路上的消息", readStatus: 0, groupSeq: 8, timestamp: 205 },
    }, 1, () => undefined);

    expect(useChatStore.getState().conversations).toEqual([]);
    expect(useChatStore.getState().messagesByConversation.g_22).toBeUndefined();
    expect(getGroup).not.toHaveBeenCalled();
    expect(send).toHaveBeenCalledWith({ type: "deliverAck", data: { serverMsgId: 9001 } });
  });

  it("prunes group conversations that are absent from an authoritative group-list refresh", async () => {
    useChatStore.getState().initializeLive(1);
    useChatStore.getState().addGroupConversation(22, group.name);
    useChatStore.getState().addGroupConversation(23, "已经退出的群");
    queryClient.setQueryData(["group", 23], { ...group, id: 23 });
    vi.spyOn(groupsApi, "list").mockResolvedValue([group]);

    await refreshGroupConversations(1);

    expect(useChatStore.getState().conversations.map((conversation) => conversation.id)).toEqual(["g_22"]);
    expect(queryClient.getQueryData(["group", 23])).toBeUndefined();
  });

  it("revalidates the authoritative group list after WebSocket reconnect", async () => {
    useChatStore.getState().initializeLive(1);
    useChatStore.getState().addGroupConversation(23, "断线期间退出的群");
    const list = vi.spyOn(groupsApi, "list").mockResolvedValue([]);

    handleConnectionState("connected");

    await waitFor(() => expect(list).toHaveBeenCalledOnce());
    await waitFor(() => expect(useChatStore.getState().conversations).toEqual([]));
  });

  it("ignores an older same-user group-list response that finishes last", async () => {
    useChatStore.getState().initializeLive(1);
    let resolveOlder!: (groups: Group[]) => void;
    let resolveNewer!: (groups: Group[]) => void;
    vi.spyOn(groupsApi, "list")
      .mockImplementationOnce(() => new Promise<Group[]>((resolve) => { resolveOlder = resolve; }))
      .mockImplementationOnce(() => new Promise<Group[]>((resolve) => { resolveNewer = resolve; }));

    const older = refreshGroupConversations(1);
    const newer = refreshGroupConversations(1);
    resolveNewer([]);
    await newer;
    resolveOlder([group]);
    await older;

    expect(useChatStore.getState().conversations).toEqual([]);
  });
});
