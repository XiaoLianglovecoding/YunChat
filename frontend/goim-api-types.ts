/** GoIM HTTP contract v1. See backend/docs/前端接口契约.md. */

export type ApiId = number;
export type IsoDateTime = string;

export interface ApiResponse<T = undefined> {
  code: number;
  message: string;
  data?: T;
}

export interface Pagination { total: number; offset: number; limit: number; has_more: boolean; }
export interface Page<T> { items: T[]; pagination: Pagination; }

export interface RegisterRequest { username: string; password: string; }
export interface RegisterResponse { user_id: ApiId; username: string; }
export interface LoginResponse { access_token: string; refresh_token: string; expires_in: number; avatar_url?: string; }
export interface RefreshRequest { refresh_token: string; }
export interface UpdateUsernameRequest { username: string; }
export interface UpdatePasswordRequest { current_password: string; new_password: string; }

export interface FriendRequest { id: ApiId; from_user_id: ApiId; to_user_id: ApiId; username?: string; avatar_url?: string; message: string; status: 0 | 1 | 2; created_at: IsoDateTime; updated_at: IsoDateTime; }
export interface Friendship { id: ApiId; user_id: ApiId; friend_id: ApiId; nickname?: string; avatar_url?: string; online: boolean; is_blocked: boolean; created_at: IsoDateTime; }
export interface Group { id: ApiId; name: string; notice: string; owner_id: ApiId; max_members: number; created_at: IsoDateTime; updated_at: IsoDateTime; }
export interface GroupMember { id: ApiId; group_id: ApiId; user_id: ApiId; role: 0 | 1 | 2; username: string; avatar_url?: string; muted_until?: IsoDateTime | null; joined_at: IsoDateTime; }
export interface Moment { id: ApiId; author_id: ApiId; author_name: string; author_avatar?: string; content: string; media_urls?: string | null; visibility: 1 | 2 | 3; created_at: IsoDateTime; like_count: number; liked_by_me: boolean; comments: MomentComment[]; }
export interface MomentComment { id: ApiId; moment_id: ApiId; user_id: ApiId; username: string; avatar_url?: string; content: string; created_at: IsoDateTime; }
export interface MomentLiker { user_id: ApiId; username: string; avatar_url?: string; }
export interface UserSettings { id: ApiId; user_id: ApiId; notification_enabled: boolean; msg_preview_enabled: boolean; mute_list: string; created_at: IsoDateTime; updated_at: IsoDateTime; }
export interface UploadAvatarResponse { url: string; file_path: string; size: number; }

export interface CreateGroupRequest { name: string; notice?: string; }
export interface CreateGroupResponse { group_id: ApiId; }
export interface UpdateGroupRequest { name: string; notice: string; }
export interface AddGroupMemberRequest { member_id: ApiId; }
export interface UpdateGroupMemberRoleRequest { role: 0 | 1; }
export interface TransferGroupOwnerRequest { new_owner_id: ApiId; }
export interface PublishMomentRequest { content: string; media_urls?: string; visibility: 2 | 3; }
export interface PublishMomentResponse { moment_id: ApiId; }
export interface MomentActionResponse { ok: boolean; liked: boolean; count: number; }
export interface MomentLikersResponse { items: MomentLiker[]; }
export interface CommentMomentRequest { content: string; }
export interface CommentMomentResponse { comment_id: ApiId; }
export interface MomentFeedResponse { moments: Moment[]; next_cursor: string; }
export interface UpdateSettingsRequest { notification_enabled: boolean; msg_preview_enabled: boolean; mute_list: string; }
export interface MuteConversationRequest { convId: string; }

export interface SendFriendRequestRequest { to_user_id: ApiId; message?: string; }
export interface SendFriendRequestResponse { request_id: ApiId; from_user_id: ApiId; to_user_id: ApiId; status: 0; }
export interface FriendRequestActionRequest { request_id: ApiId; }
export interface AcceptFriendRequestResponse { user_id: ApiId; friend_id: ApiId; }
export interface BlockUserRequest { blocked_id: ApiId; }

export interface RevokeMessageRequest { convId: string; msgId: ApiId; }
export interface SearchMessagesQuery { q: string; limit?: number; offset?: number; }
export interface PrivateMessage { msgId: ApiId; fromId: ApiId; toId: ApiId; content: string; msgType: number; timestamp: IsoDateTime; }

export interface HealthResponse { status: "ok"; service: "goim"; }
