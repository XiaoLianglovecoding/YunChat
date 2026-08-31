import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Group, Page, GroupMember } from "../../goim-api-types";
import { GroupManagementDrawer } from "../features/groups/GroupManagement";
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

describe("group profile management", () => {
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
});
