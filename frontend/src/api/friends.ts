import type { AcceptFriendRequestResponse, ApiId, BlockUserRequest, FriendRequest, FriendRequestActionRequest, Friendship, Page, SendFriendRequestRequest, SendFriendRequestResponse } from "../../goim-api-types";
import type { GoIMApiClient } from "./client";

export const createFriendsApi = (client: GoIMApiClient) => ({
  request: (input: SendFriendRequestRequest) => client.post<SendFriendRequestResponse>("/friend/request", input),
  accept: (input: FriendRequestActionRequest) => client.post<AcceptFriendRequestResponse>("/friend/accept", input),
  reject: (input: FriendRequestActionRequest) => client.post<void>("/friend/reject", input),
  requests: (limit = 20, offset = 0) => client.get<Page<FriendRequest>>(`/friend/requests?limit=${limit}&offset=${offset}`),
  list: (limit = 20, offset = 0) => client.get<Page<Friendship>>(`/friend/list?limit=${limit}&offset=${offset}`),
  remove: (friendId: ApiId) => client.delete<void>(`/friend/${friendId}`),
  block: (input: BlockUserRequest) => client.post<void>("/friend/block", input),
  unblock: (input: BlockUserRequest) => client.post<void>("/friend/unblock", input),
});
