import { useEffect } from "react";
import type { QueryClient } from "@tanstack/react-query";
import type { Friendship, Page } from "../../../goim-api-types";
import type { ServerWsMessage } from "../../../goim-ws-types";
import { buildPrivateConvId } from "../../../goim-ws-types";
import { useAuthStore } from "../../stores/authStore";
import { useChatStore } from "../../stores/chatStore";
import { goimSocket, type ConnectionState } from "../../realtime/socket";
import { friendsApi, groupsApi, settingsApi } from "../../lib/api";
import { queryClient } from "../../lib/queryClient";
import { configureNotifications, notifyIncomingMessage } from "../../realtime/notifications";

let loadedSettings: Awaited<ReturnType<typeof settingsApi.get>> | null = null;
let groupRefreshGeneration = 0;

function applyMutedConversations() {
  if (!loadedSettings) return;
  let mutedIds = new Set<string>();
  try {
    const parsed = JSON.parse(loadedSettings.mute_list) as unknown;
    if (Array.isArray(parsed)) mutedIds = new Set(parsed.filter((item): item is string => typeof item === "string"));
  } catch { /* Invalid legacy values behave as an empty list. */ }
  const chat = useChatStore.getState();
  for (const conversation of chat.conversations) chat.setConversationMuted(conversation.id, mutedIds.has(conversation.id));
}

export async function refreshPrivateConversationIdentities() {
  try {
    const friends: Friendship[] = [];
    let offset = 0;
    while (true) {
      const page = await friendsApi.list(100, offset);
      friends.push(...page.items);
      if (!page.pagination.has_more || page.items.length === 0) break;
      offset = page.pagination.offset + page.items.length;
    }
    const chat = useChatStore.getState();
    const friendIDs = new Set(friends.map((friend) => friend.friend_id));
    for (const conversation of chat.conversations) {
      if (!conversation.group && !friendIDs.has(conversation.targetId)) chat.removeConversation(conversation.id);
    }
    for (const friend of friends) {
      const conversation = chat.conversations.find((item) => !item.group && item.targetId === friend.friend_id);
      const name = friend.nickname || `用户 #${friend.friend_id}`;
      if (conversation) chat.setConversationIdentity(conversation.id, name, friend.avatar_url, friend.online);
      else if (chat.liveUserId) chat.addPrivateConversation(buildPrivateConvId(chat.liveUserId, friend.friend_id), friend.friend_id, name, friend.avatar_url);
    }
  } catch {
    // 在线状态刷新失败不影响现有会话和消息收发。
  }
}

export function invalidateFriendQueries(client: QueryClient = queryClient) {
  void client.invalidateQueries({ queryKey: ["friends"] });
  void client.invalidateQueries({ queryKey: ["friend-requests"] });
}

function updateFriendPresence(userId: number, online: boolean, client: QueryClient = queryClient) {
  client.setQueriesData<Page<Friendship>>({ queryKey: ["friends"] }, (current) => current ? {
    ...current,
    items: current.items.map((friend) => friend.friend_id === userId ? { ...friend, online } : friend),
  } : current);

  const chat = useChatStore.getState();
  const conversation = chat.conversations.find((item) => !item.group && item.targetId === userId);
  if (conversation) chat.setConversationIdentity(conversation.id, conversation.name, conversation.avatarUrl, online);
}

export function invalidateGroupQueries(groupId: number, client: QueryClient = queryClient) {
  void client.invalidateQueries({ queryKey: ["group", groupId] });
  void client.invalidateQueries({ queryKey: ["group-members", groupId] });
}

function removeGroupQueries(groupId: number, client: QueryClient = queryClient) {
  client.removeQueries({ queryKey: ["group", groupId], exact: true });
  client.removeQueries({ queryKey: ["group-members", groupId], exact: true });
}

export function handleServerMessage(message: ServerWsMessage, currentUserId: number, clearSession: () => void) {
  const chat = useChatStore.getState();
  switch (message.type) {
    case "serverAck":
      chat.acknowledge(message.data);
      break;
    case "msg":
      if (message.data.convType === 2 && chat.dissolvedGroupIds.includes(message.data.toId)) {
        // 已解散群的旧消息仍需确认送达，否则服务端会不断重投；但绝不能让
        // 它进入消息仓库、触发通知或异步补全群资料并复活会话。
        goimSocket.send({ type: "deliverAck", data: { serverMsgId: message.data.msgId } });
        break;
      }
      if (message.data.fromId !== currentUserId) {
        const conversation = chat.conversations.find((item) => item.id === message.data.convId);
        notifyIncomingMessage({ convId: message.data.convId, title: conversation?.name ?? (message.data.convType === 2 ? "群聊新消息" : "好友新消息"), content: message.data.content });
      }
      chat.receiveMessage(message.data, currentUserId);
      goimSocket.send({ type: "deliverAck", data: { serverMsgId: message.data.msgId } });
      if (message.data.convType === 1) {
        const targetId = message.data.fromId === currentUserId ? message.data.toId : message.data.fromId;
        void friendsApi.list(100, 0).then((page) => {
          const friend = page.items.find((item) => item.friend_id === targetId);
          if (friend) useChatStore.getState().setConversationIdentity(message.data.convId, friend.nickname || `用户 #${targetId}`, friend.avatar_url, friend.online);
        }).catch(() => undefined);
      } else {
        void groupsApi.get(message.data.toId).then((group) => {
          useChatStore.getState().addGroupConversation(group.id, group.name);
        }).catch(() => undefined);
      }
      break;
    case "syncBatch":
      chat.applySyncBatch(message.data, currentUserId);
      if (message.data.hasMore) {
        const { lastSyncTime, lastSyncMsgId } = useChatStore.getState();
        goimSocket.send({ type: "syncReq", data: { lastSyncTime, lastSyncMsgId, batchSize: 50 } });
      }
      break;
    case "convSync":
      chat.applyConversationSync(message.data.conversations, message.data.unreadMap);
      applyMutedConversations();
      void refreshPrivateConversationIdentities();
      void refreshGroupConversations(currentUserId);
      break;
    case "msgRevoked":
      chat.revokeMessage(message.data.convId, message.data.serverMsgId);
      break;
    case "friendApply":
      void queryClient.invalidateQueries({ queryKey: ["friend-requests"] });
      break;
    case "friendAccepted": {
      invalidateFriendQueries();
      const targetId = message.data.userId === currentUserId ? message.data.friendId : message.data.userId;
      if (targetId > 0 && targetId !== currentUserId) {
        const convId = buildPrivateConvId(currentUserId, targetId);
        const existing = chat.conversations.find((item) => item.id === convId);
        const name = message.data.username.trim() || existing?.name || `用户 #${targetId}`;
        if (existing) chat.setConversationIdentity(convId, name, message.data.avatarUrl ?? existing.avatarUrl);
        else chat.addPrivateConversation(convId, targetId, name, message.data.avatarUrl);
      }
      break;
    }
    case "presence":
      updateFriendPresence(message.data.userId, message.data.online);
      break;
    case "error":
      chat.failLatestPending(message.data.message);
      break;
    case "kick":
      goimSocket.disconnect();
      clearSession();
      break;
    case "groupRemoved":
      if (message.data.reason === "dissolved") chat.markGroupDissolved(message.data.groupId);
      else chat.removeConversation(`g_${message.data.groupId}`);
      removeGroupQueries(message.data.groupId);
      // Supersede a group-list request that may have started before this
      // removal frame; otherwise its stale response could recreate the chat.
      if (currentUserId > 0) void refreshGroupConversations(currentUserId);
      break;
    case "groupAdded":
      chat.addGroupConversation(message.data.groupId, message.data.name);
      invalidateGroupQueries(message.data.groupId);
      break;
    case "groupUpdated":
      invalidateGroupQueries(message.data.groupId);
      break;
  }
}

export async function refreshGroupConversations(expectedUserId: number) {
  const generation = ++groupRefreshGeneration;
  try {
    const groups = await groupsApi.list();
    const chat = useChatStore.getState();
    // Ignore a response for a session that logged out/switched users, and also
    // ignore an older same-user request that finished after a newer refresh.
    if (generation !== groupRefreshGeneration || chat.mode !== "live" || chat.liveUserId !== expectedUserId) return;
    const currentGroupIDs = new Set(groups.map((group) => group.id));
    for (const conversation of chat.conversations) {
      if (conversation.group && !currentGroupIDs.has(conversation.targetId)) {
        chat.removeConversation(conversation.id);
        removeGroupQueries(conversation.targetId);
      }
    }
    for (const group of groups) chat.addGroupConversation(group.id, group.name);
  } catch {
    // 群列表刷新失败不影响 WebSocket 连接和已有会话。
  }
}

export function handleConnectionState(state: ConnectionState) {
  const chat = useChatStore.getState();
  chat.setConnectionState(state);
  if (state !== "connected") return;

  // A reconnect may happen after durable friend/group events were missed.
  // HTTP revalidation restores the authoritative MySQL state in that case.
  invalidateFriendQueries();
  if (chat.liveUserId) void refreshGroupConversations(chat.liveUserId);
  const { lastSyncTime, lastSyncMsgId } = chat;
  goimSocket.send({ type: "syncReq", data: { lastSyncTime, lastSyncMsgId, batchSize: 50 } });
}

export function RealtimeBootstrap() {
  const accessToken = useAuthStore((state) => state.accessToken);
  const previewMode = useAuthStore((state) => state.previewMode);
  const userId = useAuthStore((state) => state.user?.id ?? 0);
  const clearSession = useAuthStore((state) => state.clearSession);

  useEffect(() => {
    if (previewMode) {
      goimSocket.disconnect();
      useChatStore.getState().initializePreview();
      return;
    }
    if (!accessToken || !userId) {
      goimSocket.disconnect();
      useChatStore.getState().resetSession();
      return;
    }

    useChatStore.getState().initializeLive(userId);
    // GROUP-001 can restore the user's groups before conversation sync exists.
    void refreshGroupConversations(userId);
    void settingsApi.get().then((settings) => {
      loadedSettings = settings;
      configureNotifications(settings);
      applyMutedConversations();
    }).catch(() => undefined);
    goimSocket.setHandlers({
      onStateChange: handleConnectionState,
      onMessage: (message) => handleServerMessage(message, userId, clearSession),
    });
    // React StrictMode performs a synchronous setup/cleanup probe in development.
    // Deferring the real connection avoids opening and immediately closing a socket
    // before its handshake completes during that probe.
    const connectTimer = window.setTimeout(() => goimSocket.connect(accessToken), 0);
    const onlineRefreshTimer = window.setInterval(() => void refreshPrivateConversationIdentities(), 15_000);
    return () => {
      window.clearTimeout(connectTimer);
      window.clearInterval(onlineRefreshTimer);
      goimSocket.disconnect(false);
    };
  }, [accessToken, clearSession, previewMode, userId]);

  return null;
}
