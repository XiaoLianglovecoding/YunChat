import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Friendship, Page } from "../../goim-api-types";
import { buildPrivateConvId } from "../../goim-ws-types";
import { handleConnectionState, handleServerMessage, refreshPrivateConversationIdentities } from "../components/realtime/RealtimeBootstrap";
import { friendsApi } from "../lib/api";
import { queryClient } from "../lib/queryClient";
import { goimSocket } from "../realtime/socket";
import { useChatStore } from "../stores/chatStore";

function friendPage(friendId: number, online = false): Page<Friendship> {
  return {
    items: [{
      id: friendId * 10,
      user_id: 1,
      friend_id: friendId,
      nickname: `friend-${friendId}`,
      online,
      is_blocked: false,
      created_at: "2026-08-31T12:00:00Z",
    }],
    pagination: { total: 1, offset: 0, limit: 20, has_more: false },
  };
}

describe("friend realtime events", () => {
  beforeEach(() => {
    queryClient.clear();
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

  it("refreshes incoming requests after friendApply", () => {
    const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

    handleServerMessage({
      type: "friendApply",
      data: {
        requestId: 31,
        fromUserId: 2,
        username: "Alice",
        message: "hello",
        createdAt: "2026-08-31T12:30:00Z",
      },
    }, 1, () => undefined);

    expect(invalidate).toHaveBeenCalledOnce();
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["friend-requests"] });
  });

  it("refreshes friend state and creates the other user's conversation after friendAccepted", () => {
    useChatStore.getState().initializeLive(1);
    const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();

    handleServerMessage({
      type: "friendAccepted",
      data: { requestId: 41, userId: 2, friendId: 1, username: "Bob", avatarUrl: "/bob.png" },
    }, 1, () => undefined);

    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["friends"] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["friend-requests"] });
    expect(useChatStore.getState().conversations[0]).toMatchObject({
      id: buildPrivateConvId(1, 2),
      targetId: 2,
      name: "Bob",
      avatarUrl: "/bob.png",
    });
  });

  it("applies presence to every friend page and the matching conversation", () => {
    useChatStore.getState().initializeLive(1);
    useChatStore.getState().addPrivateConversation(buildPrivateConvId(1, 2), 2, "Alice", "/alice.png");
    queryClient.setQueryData(["friends"], friendPage(2));
    queryClient.setQueryData(["friends", { offset: 20 }], friendPage(2));

    handleServerMessage({ type: "presence", data: { userId: 2, online: true } }, 1, () => undefined);

    expect(queryClient.getQueryData<Page<Friendship>>(["friends"])?.items[0].online).toBe(true);
    expect(queryClient.getQueryData<Page<Friendship>>(["friends", { offset: 20 }])?.items[0].online).toBe(true);
    expect(useChatStore.getState().conversations[0]).toMatchObject({ targetId: 2, online: true });
  });

  it("refreshes friends and requests after both the first connection and a reconnect", () => {
    useChatStore.getState().initializeLive(1);
    const invalidate = vi.spyOn(queryClient, "invalidateQueries").mockResolvedValue();
    const send = vi.spyOn(goimSocket, "send").mockReturnValue(true);

    handleConnectionState("connected");
    handleConnectionState("reconnecting");
    handleConnectionState("connected");

    expect(invalidate.mock.calls.filter(([filters]) => filters?.queryKey?.[0] === "friends")).toHaveLength(2);
    expect(invalidate.mock.calls.filter(([filters]) => filters?.queryKey?.[0] === "friend-requests")).toHaveLength(2);
    expect(send).toHaveBeenCalledTimes(2);
  });

  it("loads every friend page before removing conversations", async () => {
    useChatStore.getState().initializeLive(1);
    useChatStore.getState().addPrivateConversation(buildPrivateConvId(1, 2), 2, "first");
    useChatStore.getState().addPrivateConversation(buildPrivateConvId(1, 102), 102, "second page");
    const list = vi.spyOn(friendsApi, "list")
      .mockResolvedValueOnce({
        items: friendPage(2).items,
        pagination: { total: 2, offset: 0, limit: 100, has_more: true },
      })
      .mockResolvedValueOnce({
        items: friendPage(102).items,
        pagination: { total: 2, offset: 1, limit: 100, has_more: false },
      });

    await refreshPrivateConversationIdentities();

    expect(list).toHaveBeenNthCalledWith(1, 100, 0);
    expect(list).toHaveBeenNthCalledWith(2, 100, 1);
    expect(useChatStore.getState().conversations.map((item) => item.targetId).sort((a, b) => a - b)).toEqual([2, 102]);
  });
});
