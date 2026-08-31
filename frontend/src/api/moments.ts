import type { ApiId, CommentMomentRequest, CommentMomentResponse, Moment, MomentActionResponse, MomentFeedResponse, MomentLikersResponse, Page, PublishMomentRequest, PublishMomentResponse } from "../../goim-api-types";
import type { GoIMApiClient } from "./client";

export const createMomentsApi = (client: GoIMApiClient) => ({
  publish: (input: PublishMomentRequest) => client.post<PublishMomentResponse>("/moment", input),
  get: (momentId: ApiId) => client.get<Moment>(`/moment/${momentId}`),
  byUser: (userId: ApiId, limit = 20, offset = 0) => client.get<Page<Moment>>(`/moment/user/${userId}?limit=${limit}&offset=${offset}`),
  like: (momentId: ApiId) => client.post<MomentActionResponse>(`/moment/${momentId}/like`),
  unlike: (momentId: ApiId) => client.delete<MomentActionResponse>(`/moment/${momentId}/like`),
  likers: (momentId: ApiId) => client.get<MomentLikersResponse>(`/moment/${momentId}/likers`),
  delete: (momentId: ApiId) => client.delete<void>(`/moment/${momentId}`),
  comment: (momentId: ApiId, input: CommentMomentRequest) => client.post<CommentMomentResponse>(`/moment/${momentId}/comment`, input),
  deleteComment: (commentId: ApiId) => client.delete<void>(`/moment/comment/${commentId}`),
  feed: (cursor = "", limit = 20) => client.get<MomentFeedResponse>(`/moment/feed?cursor=${encodeURIComponent(cursor)}&limit=${limit}`),
});
