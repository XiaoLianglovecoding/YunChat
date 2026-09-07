import { afterEach, describe, expect, it, vi } from "vitest";
import type { GroupMember, Page } from "../../goim-api-types";
import { fetchAllGroupMembers, groupMembersQueryKey } from "../features/groups/groupMembers";
import { groupsApi } from "../lib/api";

function member(userId: number, role: GroupMember["role"] = 0): GroupMember {
  return {
    id: userId * 10,
    group_id: 22,
    user_id: userId,
    role,
    username: `user-${userId}`,
    joined_at: "2026-08-31T12:00:00Z",
  };
}

function page(items: GroupMember[], offset: number, total: number, hasMore: boolean): Page<GroupMember> {
  return { items, pagination: { total, offset, limit: 100, has_more: hasMore } };
}

describe("group member pagination", () => {
  afterEach(() => vi.restoreAllMocks());

  it("collects every 100-row page using the server offset", async () => {
    const list = vi.spyOn(groupsApi, "members")
      .mockResolvedValueOnce(page([member(1, 2), member(2)], 0, 3, true))
      .mockResolvedValueOnce(page([member(3)], 2, 3, false));

    await expect(fetchAllGroupMembers(22)).resolves.toEqual({
      items: [member(1, 2), member(2), member(3)],
      total: 3,
    });
    expect(list).toHaveBeenNthCalledWith(1, 22, 100, 0);
    expect(list).toHaveBeenNthCalledWith(2, 22, 100, 2);
    expect(groupMembersQueryKey(22)).toEqual(["group-members", 22]);
  });

  it("rejects a has-more page that cannot advance instead of looping forever", async () => {
    const list = vi.spyOn(groupsApi, "members").mockResolvedValue(page([], 0, 1, true));

    await expect(fetchAllGroupMembers(22)).rejects.toThrow("群成员分页响应无效");
    expect(list).toHaveBeenCalledOnce();
  });
});
