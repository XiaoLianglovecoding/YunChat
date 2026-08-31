import type { AddGroupMemberRequest, ApiId, CreateGroupRequest, CreateGroupResponse, Group, GroupMember, Page, TransferGroupOwnerRequest, UpdateGroupMemberRoleRequest, UpdateGroupRequest } from "../../goim-api-types";
import type { GoIMApiClient } from "./client";

export const createGroupsApi = (client: GoIMApiClient) => ({
  create: (input: CreateGroupRequest) => client.post<CreateGroupResponse>("/group", input),
  list: () => client.get<Group[]>("/group/list"),
  get: (groupId: ApiId) => client.get<Group>(`/group/${groupId}`),
  update: (groupId: ApiId, input: UpdateGroupRequest) => client.put<void>(`/group/${groupId}`, input),
  addMember: (groupId: ApiId, input: AddGroupMemberRequest) => client.post<void>(`/group/${groupId}/member`, input),
  removeMember: (groupId: ApiId, memberId: ApiId) => client.delete<void>(`/group/${groupId}/member/${memberId}`),
  members: (groupId: ApiId, limit = 50, offset = 0) => client.get<Page<GroupMember>>(`/group/${groupId}/members?limit=${limit}&offset=${offset}`),
  updateRole: (groupId: ApiId, memberId: ApiId, input: UpdateGroupMemberRoleRequest) => client.put<void>(`/group/${groupId}/member/${memberId}/role`, input),
  transferOwner: (groupId: ApiId, input: TransferGroupOwnerRequest) => client.put<void>(`/group/${groupId}/owner`, input),
  leave: (groupId: ApiId) => client.post<void>(`/group/${groupId}/leave`),
});
