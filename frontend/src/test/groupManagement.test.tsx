import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Friendship, Group, Page, GroupMember } from "../../goim-api-types";
import { ApiError } from "../api/client";
import { canRemoveGroupMember, GroupManagementDrawer } from "../features/groups/GroupManagement";
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

function renderManagement() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const conversation = useChatStore.getState().conversations[0];
  render(
    <QueryClientProvider client={client}>
      <GroupManagementDrawer conversation={conversation} onClose={() => undefined} open />
    </QueryClientProvider>,
  );
  return client;
}

describe("group profile management", () => {
  afterEach(() => vi.restoreAllMocks());

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
});
