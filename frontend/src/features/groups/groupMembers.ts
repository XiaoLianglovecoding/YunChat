import type { ApiId, GroupMember } from "../../../goim-api-types";
import { groupsApi } from "../../lib/api";

export const GROUP_MEMBER_PAGE_LIMIT = 100;

export interface AllGroupMembers {
  items: GroupMember[];
  total: number;
}

export function groupMembersQueryKey(groupId: ApiId) {
  return ["group-members", groupId] as const;
}

/**
 * The HTTP endpoint is intentionally paginated at no more than 100 rows.
 * A group currently contains at most 500 people, so the UI can safely collect
 * every page and share one complete member snapshot between chat and the
 * management drawer.
 */
export async function fetchAllGroupMembers(groupId: ApiId): Promise<AllGroupMembers> {
  const membersByUserId = new Map<ApiId, GroupMember>();
  let offset = 0;
  let total = 0;

  while (true) {
    const page = await groupsApi.members(groupId, GROUP_MEMBER_PAGE_LIMIT, offset);
    total = page.pagination.total;
    for (const member of page.items) membersByUserId.set(member.user_id, member);

    if (!page.pagination.has_more) break;
    const nextOffset = page.pagination.offset + page.items.length;
    if (page.items.length === 0 || nextOffset <= offset) {
      throw new Error("群成员分页响应无效，请稍后重试");
    }
    offset = nextOffset;
  }

  return {
    items: Array.from(membersByUserId.values()),
    total: Math.max(total, membersByUserId.size),
  };
}
