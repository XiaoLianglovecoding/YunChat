import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Friendship, Group, Page, GroupMember } from "../../goim-api-types";
import { ApiError } from "../api/client";
import { canDissolveGroup, canLeaveGroup, canMuteGroupMember, canRemoveGroupMember, canTransferGroupOwnership, canUpdateGroupMemberRole, describeGroupMemberMute, GroupManagementDrawer, isGroupMemberMuted } from "../features/groups/GroupManagement";
import { friendsApi, groupsApi } from "../lib/api";
import { useAuthStore } from "../stores/authStore";
import { useChatStore } from "../stores/chatStore";

const group: Group = {
  id: 22,
  name: "旧群名",
  notice: "旧公告",
  owner_id: 1,
  max_members: 500,
  created_at: "2026-08-31T12:00:00Z",
  updated_at: "2026-08-31T12:00:00Z",
};

const emptyMembers: Page<GroupMember> = {
  items: [],
  pagination: { total: 0, offset: 0, limit: 100, has_more: false },
};

function member(userId: number, role: GroupMember["role"], username = `member-${userId}`): GroupMember {
  return {
    id: userId * 10,
    group_id: group.id,
    user_id: userId,
    role,
    username,
    joined_at: "2026-08-31T12:00:00Z",
  };
}

function friend(friendId: number, nickname = `friend-${friendId}`): Friendship {
  return {
    id: friendId * 100,
    user_id: 1,
    friend_id: friendId,
    nickname,
    online: false,
    is_blocked: false,
    created_at: "2026-08-31T12:00:00Z",
  };
}

function page(items: GroupMember[], total = items.length, offset = 0, hasMore = false): Page<GroupMember> {
  return { items, pagination: { total, offset, limit: 100, has_more: hasMore } };
}

function ManagementHarness({ conversation, onClose }: { conversation: ReturnType<typeof useChatStore.getState>["conversations"][number]; onClose: () => void }) {
  const [open, setOpen] = useState(true);
  if (!open) return null;
  return <GroupManagementDrawer conversation={conversation} onClose={() => { setOpen(false); onClose(); }} open />;
}

function renderManagement(onClose = () => undefined) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const conversation = useChatStore.getState().conversations[0];
  render(
    <QueryClientProvider client={client}>
      <ManagementHarness conversation={conversation} onClose={onClose} />
    </QueryClientProvider>,
  );
  return client;
}

describe("group profile management", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  beforeEach(() => {
    useAuthStore.setState({
      accessToken: "access-token",
      refreshToken: "refresh-token",
      accessTokenExpiresAt: Date.now() + 60_000,
      user: { id: 1, username: "owner" },
      previewMode: false,
    });
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
    useChatStore.getState().initializeLive(1);
    useChatStore.getState().addGroupConversation(22, group.name);
  });

  it("lets the owner edit without member data and synchronizes the renamed conversation", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(emptyMembers);
    const update = vi.spyOn(groupsApi, "update").mockResolvedValue(undefined);
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [],
      pagination: { total: 0, offset: 0, limit: 100, has_more: false },
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const conversation = useChatStore.getState().conversations[0];

    render(
      <QueryClientProvider client={client}>
        <GroupManagementDrawer conversation={conversation} onClose={() => undefined} open />
      </QueryClientProvider>,
    );

    fireEvent.click(await screen.findByRole("button", { name: "编辑资料" }));
    fireEvent.change(screen.getByLabelText("群名称"), { target: { value: "  新群名  " } });
    fireEvent.click(screen.getByRole("button", { name: "保存更改" }));

    await waitFor(() => expect(update).toHaveBeenCalledWith(22, { name: "新群名", notice: "旧公告" }));
    await waitFor(() => expect(useChatStore.getState().conversations[0].name).toBe("新群名"));
  });

  it("applies the owner and administrator removal matrix", () => {
    const owner = member(1, 2);
    const admin = member(2, 1);
    const peerAdmin = member(3, 1);
    const ordinary = member(4, 0);

    expect(canRemoveGroupMember(group, owner, 1, admin)).toBe(true);
    expect(canRemoveGroupMember(group, owner, 1, ordinary)).toBe(true);
    expect(canRemoveGroupMember(group, admin, 2, ordinary)).toBe(true);
    expect(canRemoveGroupMember(group, admin, 2, peerAdmin)).toBe(false);
    expect(canRemoveGroupMember(group, admin, 2, owner)).toBe(false);
    expect(canRemoveGroupMember(group, admin, 2, admin)).toBe(false);
    expect(canRemoveGroupMember(group, ordinary, 4, admin)).toBe(false);
  });

  it("applies the role and mute permission matrix using the real group owner", () => {
    const owner = member(1, 2);
    const admin = member(2, 1);
    const peerAdmin = member(3, 1);
    const ordinary = member(4, 0);

    expect(canUpdateGroupMemberRole(group, 1, admin)).toBe(true);
    expect(canUpdateGroupMemberRole(group, 1, ordinary)).toBe(true);
    expect(canUpdateGroupMemberRole(group, 1, owner)).toBe(false);
    expect(canUpdateGroupMemberRole(group, 2, ordinary)).toBe(false);

    expect(canMuteGroupMember(group, owner, 1, admin)).toBe(true);
    expect(canMuteGroupMember(group, owner, 1, ordinary)).toBe(true);
    expect(canMuteGroupMember(group, owner, 1, owner)).toBe(false);
    expect(canMuteGroupMember(group, admin, 2, ordinary)).toBe(true);
    expect(canMuteGroupMember(group, admin, 2, peerAdmin)).toBe(false);
    expect(canMuteGroupMember(group, admin, 2, owner)).toBe(false);
    expect(canMuteGroupMember(group, ordinary, 4, ordinary)).toBe(false);
  });

  it("uses groups.owner_id for the transfer and leave permission matrix", () => {
    const owner = member(1, 2);
    const admin = member(2, 1);
    const ordinary = member(3, 0);
    const staleOwnerRole = member(4, 2);

    expect(canTransferGroupOwnership(group, 1, admin)).toBe(true);
    expect(canTransferGroupOwnership(group, 1, ordinary)).toBe(true);
    expect(canTransferGroupOwnership(group, 1, staleOwnerRole)).toBe(true);
    expect(canTransferGroupOwnership(group, 1, owner)).toBe(false);
    expect(canTransferGroupOwnership(group, 2, ordinary)).toBe(false);
    expect(canTransferGroupOwnership(group, 1, { ...ordinary, group_id: 999 })).toBe(false);

    expect(canLeaveGroup(group, owner, 1)).toBe(false);
    expect(canLeaveGroup(group, admin, 2)).toBe(true);
    expect(canLeaveGroup(group, ordinary, 3)).toBe(true);
    expect(canLeaveGroup(group, staleOwnerRole, 4)).toBe(true);
    expect(canLeaveGroup(group, undefined, 2)).toBe(false);
    expect(canDissolveGroup(group, 1)).toBe(true);
    expect(canDissolveGroup(group, 2)).toBe(false);
  });

  it("recognizes active and expired mute deadlines", () => {
    const muted = { ...member(4, 0), muted_until: "2026-08-31T13:00:00Z" };

    expect(isGroupMemberMuted(muted, Date.parse("2026-08-31T12:00:00Z"))).toBe(true);
    expect(isGroupMemberMuted(muted, Date.parse("2026-08-31T13:00:00Z"))).toBe(false);
    expect(describeGroupMemberMute(muted, Date.parse("2026-08-31T12:00:00Z"))).toContain("禁言至");
    expect(describeGroupMemberMute(muted, Date.parse("2026-08-31T14:00:00Z"))).toContain("到期");
  });

  it("collects all member pages, displays the total, and excludes every existing member from invitations", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    const listMembers = vi.spyOn(groupsApi, "members")
      .mockResolvedValueOnce(page([member(1, 2, "群主"), member(2, 0, "第一页成员")], 3, 0, true))
      .mockResolvedValueOnce(page([member(3, 0, "第二页成员")], 3, 2, false));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [friend(3, "第二页已入群"), friend(4, "可邀请用户")],
      pagination: { total: 2, offset: 0, limit: 100, has_more: false },
    });

    renderManagement();

    expect(await screen.findByText("3 位成员 · 最多 500 人")).toBeInTheDocument();
    expect(await screen.findByText("第二页成员")).toBeInTheDocument();
    expect(screen.queryByText("第二页已入群")).not.toBeInTheDocument();
    expect(screen.getByText("可邀请用户")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "添加" })).toHaveLength(1);
    expect(listMembers).toHaveBeenNthCalledWith(1, 22, 100, 0);
    expect(listMembers).toHaveBeenNthCalledWith(2, 22, 100, 2);
  });

  it("shows a full-group state and does not expose add buttons", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue({ ...group, max_members: 2 });
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2), member(2, 0)], 2));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [friend(3)],
      pagination: { total: 1, offset: 0, limit: 100, has_more: false },
    });

    renderManagement();

    expect(await screen.findByText("群成员已达上限，暂时不能继续邀请。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "添加" })).not.toBeInTheDocument();
  });

  it("maps member-list business errors and lets the user retry", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members")
      .mockRejectedValueOnce(new ApiError("not a group member", 5001, 403))
      .mockResolvedValueOnce(page([member(1, 2, "群主用户")]));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [],
      pagination: { total: 0, offset: 0, limit: 100, has_more: false },
    });

    renderManagement();

    expect(await screen.findByText("你已经不在这个群聊中")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("群主用户（我）")).toBeInTheDocument();
  });

  it("prevents duplicate invitations while the first request is pending", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2)]));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [friend(2, "待邀请好友")],
      pagination: { total: 1, offset: 0, limit: 100, has_more: false },
    });
    let resolveAdd!: () => void;
    const add = vi.spyOn(groupsApi, "addMember").mockImplementation(() => new Promise<void>((resolve) => { resolveAdd = resolve; }));

    renderManagement();
    const addButton = await screen.findByRole("button", { name: "添加" });
    fireEvent.click(addButton);
    fireEvent.click(addButton);

    await waitFor(() => expect(add).toHaveBeenCalledOnce());
    expect(add).toHaveBeenCalledWith(22, { member_id: 2 });
    expect(addButton).toBeDisabled();
    resolveAdd();
    await waitFor(() => expect(addButton).not.toBeDisabled());
  });

  it("maps an add-member capacity error to a clear Chinese message", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2)]));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [friend(2, "待邀请好友")],
      pagination: { total: 1, offset: 0, limit: 100, has_more: false },
    });
    vi.spyOn(groupsApi, "addMember").mockRejectedValue(new ApiError("group is full", 1304, 409));

    renderManagement();
    fireEvent.click(await screen.findByRole("button", { name: "添加" }));

    expect(await screen.findByText("群成员已达上限")).toBeInTheDocument();
  });

  it("lets the real owner confirm removal of an administrator", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "群主"), member(2, 1, "管理员")], 2));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [],
      pagination: { total: 0, offset: 0, limit: 100, has_more: false },
    });
    const remove = vi.spyOn(groupsApi, "removeMember").mockResolvedValue(undefined);

    renderManagement();
    fireEvent.click(await screen.findByRole("button", { name: "移除成员" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "移除成员" }));

    await waitFor(() => expect(remove).toHaveBeenCalledWith(22, 2));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
  });

  it("lets only the real owner promote an ordinary member", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "群主"), member(3, 0, "待提升成员")], 2));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [],
      pagination: { total: 0, offset: 0, limit: 100, has_more: false },
    });
    const updateRole = vi.spyOn(groupsApi, "updateRole").mockResolvedValue(undefined);

    renderManagement();
    fireEvent.click(await screen.findByRole("button", { name: "设为管理员" }));
    expect(updateRole).not.toHaveBeenCalled();
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "设为管理员" }));

    await waitFor(() => expect(updateRole).toHaveBeenCalledWith(22, 3, { role: 1 }));
  });

  it("lets the real owner confirm a transfer and synchronizes group and member query state", async () => {
    const beforeMembers = page([member(1, 2, "旧群主"), { ...member(3, 1, "新群主候选人"), muted_until: "2099-01-01T00:00:00Z" }], 2);
    const afterGroup = { ...group, owner_id: 3 };
    const afterMembers = page([member(1, 0, "旧群主"), member(3, 2, "新群主候选人")], 2);
    vi.spyOn(groupsApi, "get").mockResolvedValueOnce(group).mockResolvedValue(afterGroup);
    vi.spyOn(groupsApi, "members").mockResolvedValueOnce(beforeMembers).mockResolvedValue(afterMembers);
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [],
      pagination: { total: 0, offset: 0, limit: 100, has_more: false },
    });
    const transfer = vi.spyOn(groupsApi, "transferOwner").mockResolvedValue(undefined);
    const client = renderManagement();
    const invalidate = vi.spyOn(client, "invalidateQueries");

    fireEvent.click(await screen.findByRole("button", { name: "转让群主" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/新群主候选人 将成为新群主/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "转让群主" }));

    await waitFor(() => expect(transfer).toHaveBeenCalledWith(22, { new_owner_id: 3 }));
    await waitFor(() => expect(client.getQueryData<Group>(["group", 22])?.owner_id).toBe(3));
    await waitFor(() => expect(client.getQueryData<{ items: GroupMember[] }>(["group-members", 22])?.items).toEqual(expect.arrayContaining([
      expect.objectContaining({ user_id: 1, role: 0 }),
      expect.objectContaining({ user_id: 3, role: 2 }),
    ])));
    expect(client.getQueryData<{ items: GroupMember[] }>(["group-members", 22])?.items.find((item) => item.user_id === 3)?.muted_until).toBeUndefined();
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["group", 22] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["group-members", 22] });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(screen.queryByRole("button", { name: "转让群主" })).not.toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "退出群聊" })).toBeInTheDocument();
    expect(useChatStore.getState().conversations).toHaveLength(1);
  });

  it("lets a non-owner leave and clears its conversation, messages, and group queries", async () => {
    useAuthStore.setState({ user: { id: 2, username: "member" } });
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "群主"), member(2, 0, "准备退群")], 2));
    const leave = vi.spyOn(groupsApi, "leave").mockResolvedValue(undefined);
    vi.spyOn(groupsApi, "list").mockResolvedValue([]);
    const onClose = vi.fn();
    const client = renderManagement(onClose);

    const leaveButton = await screen.findByRole("button", { name: "退出群聊" });
    fireEvent.click(leaveButton);
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/本地会话和消息会被移除/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "退出群聊" }));

    await waitFor(() => expect(leave).toHaveBeenCalledWith(22));
    await waitFor(() => expect(useChatStore.getState().conversations).toHaveLength(0));
    expect(useChatStore.getState().messagesByConversation.g_22).toBeUndefined();
    await waitFor(() => expect(client.getQueryData(["group", 22])).toBeUndefined());
    expect(client.getQueryData(["group-members", 22])).toBeUndefined();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("treats an already-absent membership as a successful leave retry", async () => {
    useAuthStore.setState({ user: { id: 2, username: "member" } });
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "群主"), member(2, 0, "已经退出")], 2));
    vi.spyOn(groupsApi, "leave").mockRejectedValue(new ApiError("not a group member", 5001, 403));
    vi.spyOn(groupsApi, "list").mockResolvedValue([]);
    const onClose = vi.fn();
    const client = renderManagement(onClose);

    fireEvent.click(await screen.findByRole("button", { name: "退出群聊" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "退出群聊" }));

    await waitFor(() => expect(useChatStore.getState().conversations).toHaveLength(0));
    expect(client.getQueryData(["group", 22])).toBeUndefined();
    expect(client.getQueryData(["group-members", 22])).toBeUndefined();
    expect(onClose).toHaveBeenCalledOnce();
    expect(screen.queryByText("你已经不在这个群聊中")).not.toBeInTheDocument();
  });

  it("shows dissolve instead of leave to the real owner", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "群主")], 1));
    vi.spyOn(friendsApi, "list").mockResolvedValue({ items: [], pagination: { total: 0, offset: 0, limit: 100, has_more: false } });

    renderManagement();

    expect(await screen.findByText("群主不能直接退出；你可以先转让群主，或者解散整个群聊。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "退出群聊" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "解散群聊" })).toBeInTheDocument();
  });

  it("lets the real owner confirm dissolution and clears local group state", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "群主"), member(2, 0, "成员")], 2));
    vi.spyOn(friendsApi, "list").mockResolvedValue({ items: [], pagination: { total: 0, offset: 0, limit: 100, has_more: false } });
    const dissolve = vi.spyOn(groupsApi, "dissolve").mockResolvedValue(undefined);
    vi.spyOn(groupsApi, "list").mockResolvedValue([]);
    const onClose = vi.fn();
    const client = renderManagement(onClose);

    fireEvent.click(await screen.findByRole("button", { name: "解散群聊" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/所有成员都会被移出/)).toBeInTheDocument();
    expect(within(dialog).getByText(/此操作不可撤销/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "确认解散" }));

    await waitFor(() => expect(dissolve).toHaveBeenCalledWith(22));
    await waitFor(() => expect(useChatStore.getState().conversations).toHaveLength(0));
    expect(useChatStore.getState().messagesByConversation.g_22).toBeUndefined();
    expect(useChatStore.getState().dissolvedGroupIds).toEqual([22]);
    await waitFor(() => expect(client.getQueryData(["group", 22])).toBeUndefined());
    expect(client.getQueryData(["group-members", 22])).toBeUndefined();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("treats group-not-found as a successful dissolution retry", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "群主")], 1));
    vi.spyOn(friendsApi, "list").mockResolvedValue({ items: [], pagination: { total: 0, offset: 0, limit: 100, has_more: false } });
    vi.spyOn(groupsApi, "dissolve").mockRejectedValue(new ApiError("group not found", 1302, 404));
    vi.spyOn(groupsApi, "list").mockResolvedValue([]);
    const onClose = vi.fn();
    const client = renderManagement(onClose);

    fireEvent.click(await screen.findByRole("button", { name: "解散群聊" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "确认解散" }));

    await waitFor(() => expect(useChatStore.getState().conversations).toHaveLength(0));
    expect(client.getQueryData(["group", 22])).toBeUndefined();
    expect(client.getQueryData(["group-members", 22])).toBeUndefined();
    expect(onClose).toHaveBeenCalledOnce();
    expect(screen.queryByText("这个群聊不存在或已被解散")).not.toBeInTheDocument();
  });

  it("keeps local state and shows a stable message when dissolution permission is stale", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "缓存里的群主")], 1));
    vi.spyOn(friendsApi, "list").mockResolvedValue({ items: [], pagination: { total: 0, offset: 0, limit: 100, has_more: false } });
    vi.spyOn(groupsApi, "dissolve").mockRejectedValue(new ApiError("permission denied", 1301, 403));

    renderManagement();
    fireEvent.click(await screen.findByRole("button", { name: "解散群聊" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "确认解散" }));

    expect(await screen.findByText("只有当前群主才能解散群聊")).toBeInTheDocument();
    expect(useChatStore.getState().conversations).toHaveLength(1);
    expect(useChatStore.getState().messagesByConversation.g_22).toBeDefined();
  });

  it("maps a stale-owner leave rejection without deleting the conversation", async () => {
    useAuthStore.setState({ user: { id: 2, username: "member" } });
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "服务端新群主"), member(2, 0, "缓存里的普通成员")], 2));
    vi.spyOn(groupsApi, "leave").mockRejectedValue(new ApiError("owner must transfer ownership first", 1306, 409));

    renderManagement();
    fireEvent.click(await screen.findByRole("button", { name: "退出群聊" }));
    fireEvent.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "退出群聊" }));

    expect(await screen.findByText("群主不能直接退出，请先转让群主身份")).toBeInTheDocument();
    expect(useChatStore.getState().conversations).toHaveLength(1);
  });

  it("automatically changes an active mute to expired while the drawer stays open", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-31T12:00:00Z"));
    const mutedMember = { ...member(3, 0, "即将解禁成员"), muted_until: "2026-08-31T12:00:01Z" };
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
    client.setQueryData(["group", 22], group);
    client.setQueryData(["group-members", 22], { items: [member(1, 2, "群主"), mutedMember], total: 2 });
    client.setQueryData(["friends"], { items: [], pagination: { total: 0, offset: 0, limit: 100, has_more: false } });
    const conversation = useChatStore.getState().conversations[0];

    render(
      <QueryClientProvider client={client}>
        <GroupManagementDrawer conversation={conversation} onClose={() => undefined} open />
      </QueryClientProvider>,
    );
    expect(screen.getByRole("button", { name: "解除禁言" })).toBeInTheDocument();

    await act(async () => { vi.advanceTimersByTime(1_100); });

    expect(screen.getByRole("button", { name: "禁言成员" })).toBeInTheDocument();
    expect(screen.getByText(/禁言已于.*到期/)).toBeInTheDocument();
  });

  it("lets the owner choose a mute duration and refreshes the member cache", async () => {
    vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-08-31T12:00:00Z"));
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "群主"), member(3, 0, "需要安静一下")], 2));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [],
      pagination: { total: 0, offset: 0, limit: 100, has_more: false },
    });
    let resolveMute!: () => void;
    const mute = vi.spyOn(groupsApi, "muteMember").mockImplementation(() => new Promise<void>((resolve) => { resolveMute = resolve; }));
    const client = renderManagement();
    const invalidate = vi.spyOn(client, "invalidateQueries");

    fireEvent.click(await screen.findByRole("button", { name: "禁言成员" }));
    const durationPanel = screen.getByRole("group", { name: "选择禁言时长" });
    const oneHour = within(durationPanel).getByRole("button", { name: "1 小时" });
    fireEvent.click(oneHour);

    await waitFor(() => expect(mute).toHaveBeenCalledWith(22, 3, { muted_until: "2026-08-31T13:00:00.000Z" }));
    expect(oneHour).toBeDisabled();
    resolveMute();
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["group-members", 22] }));
    await waitFor(() => expect(screen.queryByRole("group", { name: "选择禁言时长" })).not.toBeInTheDocument());
  });

  it("displays muted_until and lets the owner unmute a member", async () => {
    const mutedMember = { ...member(3, 0, "被禁言成员"), muted_until: "2099-08-31T13:00:00Z" };
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "群主"), mutedMember], 2));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [],
      pagination: { total: 0, offset: 0, limit: 100, has_more: false },
    });
    const unmute = vi.spyOn(groupsApi, "unmuteMember").mockResolvedValue(undefined);

    renderManagement();

    expect(await screen.findByText(/禁言至/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "解除禁言" }));
    await waitFor(() => expect(unmute).toHaveBeenCalledWith(22, 3));
  });

  it("only exposes ordinary-member mute controls to an administrator", async () => {
    useAuthStore.setState({ user: { id: 2, username: "admin" } });
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([
      member(1, 2, "群主"),
      member(2, 1, "当前管理员"),
      member(3, 1, "同级管理员"),
      member(4, 0, "普通成员"),
    ], 4));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [],
      pagination: { total: 0, offset: 0, limit: 100, has_more: false },
    });

    renderManagement();

    expect(await screen.findAllByRole("button", { name: "禁言成员" })).toHaveLength(1);
    expect(screen.queryByRole("button", { name: "设为管理员" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "取消管理员" })).not.toBeInTheDocument();
  });

  it("maps a mute permission failure to a clear Chinese message", async () => {
    vi.spyOn(groupsApi, "get").mockResolvedValue(group);
    vi.spyOn(groupsApi, "members").mockResolvedValue(page([member(1, 2, "群主"), member(3, 0, "普通成员")], 2));
    vi.spyOn(friendsApi, "list").mockResolvedValue({
      items: [],
      pagination: { total: 0, offset: 0, limit: 100, has_more: false },
    });
    vi.spyOn(groupsApi, "muteMember").mockRejectedValue(new ApiError("permission denied", 1301, 403));

    renderManagement();
    fireEvent.click(await screen.findByRole("button", { name: "禁言成员" }));
    fireEvent.click(screen.getByRole("button", { name: "10 分钟" }));

    expect(await screen.findByText("你无权操作该成员")).toBeInTheDocument();
  });
});
