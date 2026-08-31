import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import type { FriendRequest } from "../../../goim-api-types";
import { contacts as previewContacts } from "../../mocks/data";
import { friendsApi } from "../../lib/api";
import { useAuthStore } from "../../stores/authStore";
import { useChatStore } from "../../stores/chatStore";
import { buildPrivateConvId } from "../../../goim-ws-types";

export interface FriendView {
  routeId: string;
  userId: number;
  name: string;
  avatarUrl?: string;
  note: string;
  online?: boolean;
  location?: string;
  groups?: number;
  isBlocked: boolean;
}

const previewIds: Record<string, number> = { "chen-xi": 2003, "lin-cheng": 2001, "lu-yao": 2004, "zhou-yu": 2002 };
const initialPreviewRequests: FriendRequest[] = [
  { id: 9001, from_user_id: 2010, to_user_id: 10086, message: "你好，想和你聊聊新项目", status: 0, created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
  { id: 9002, from_user_id: 2011, to_user_id: 10086, message: "我们在产品讨论组", status: 0, created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
];

export function useFriends() {
  const previewMode = useAuthStore((state) => state.previewMode);
  const currentUserId = useAuthStore((state) => state.user?.id ?? 0);
  const queryClient = useQueryClient();
  const [localContacts, setLocalContacts] = useState(previewContacts);
  const [localRequests, setLocalRequests] = useState(initialPreviewRequests);

  const friendsQuery = useQuery({ queryKey: ["friends"], queryFn: () => friendsApi.list(100, 0), enabled: !previewMode, refetchInterval: 15_000 });
  const requestsQuery = useQuery({ queryKey: ["friend-requests"], queryFn: () => friendsApi.requests(100, 0), enabled: !previewMode });
  const acceptMutation = useMutation({ mutationFn: (requestId: number) => friendsApi.accept({ request_id: requestId }), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["friends"] }) });
  const rejectMutation = useMutation({ mutationFn: (requestId: number) => friendsApi.reject({ request_id: requestId }) });
  const sendMutation = useMutation({ mutationFn: (input: { userId: number; message: string }) => friendsApi.request({ to_user_id: input.userId, message: input.message }) });
  const removeMutation = useMutation({ mutationFn: (friendId: number) => friendsApi.remove(friendId), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["friends"] }) });
  const blockMutation = useMutation({ mutationFn: (friendId: number) => friendsApi.block({ blocked_id: friendId }), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["friends"] }) });
  const unblockMutation = useMutation({ mutationFn: (friendId: number) => friendsApi.unblock({ blocked_id: friendId }), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["friends"] }) });

  const contacts: FriendView[] = useMemo(() => {
    if (previewMode) return localContacts.map((contact) => ({ routeId: contact.id, userId: previewIds[contact.id], name: contact.name, note: contact.note, online: contact.online, location: contact.location, groups: contact.groups, isBlocked: false }));
    return (friendsQuery.data?.items ?? []).map((friend) => ({ routeId: String(friend.friend_id), userId: friend.friend_id, name: friend.nickname || `用户 #${friend.friend_id}`, avatarUrl: friend.avatar_url, note: "好友", online: friend.online, isBlocked: friend.is_blocked }));
  }, [friendsQuery.data, localContacts, previewMode]);

  const requests = previewMode ? localRequests : (requestsQuery.data?.items ?? []);

  const accept = async (requestId: number) => {
    if (previewMode) { setLocalRequests((current) => current.filter((request) => request.id !== requestId)); return; }
    const response = await acceptMutation.mutateAsync(requestId);
    const acceptedFriendID = response.user_id === currentUserId ? response.friend_id : response.user_id;
    const applicant = requestsQuery.data?.items.find((request) => request.id === requestId);
    useChatStore.getState().addPrivateConversation(
      buildPrivateConvId(currentUserId, acceptedFriendID),
      acceptedFriendID,
      applicant?.username || `用户 #${acceptedFriendID}`,
      applicant?.avatar_url,
    );
    await queryClient.invalidateQueries({ queryKey: ["friend-requests"] });
    await queryClient.invalidateQueries({ queryKey: ["friends"] });
  };
  const reject = async (requestId: number) => {
    if (previewMode) { setLocalRequests((current) => current.filter((request) => request.id !== requestId)); return; }
    await rejectMutation.mutateAsync(requestId);
    await queryClient.invalidateQueries({ queryKey: ["friend-requests"] });
  };
  const sendRequest = async (userId: number, message: string) => {
    if (previewMode) return;
    await sendMutation.mutateAsync({ userId, message });
  };
  const remove = async (friend: FriendView) => {
    if (previewMode) setLocalContacts((current) => current.filter((contact) => contact.id !== friend.routeId));
    else await removeMutation.mutateAsync(friend.userId);
    useChatStore.getState().removeConversation(previewMode ? friend.routeId : buildPrivateConvId(currentUserId, friend.userId));
  };
  const block = async (friend: FriendView) => {
    if (previewMode) { setLocalContacts((current) => current.filter((contact) => contact.id !== friend.routeId)); return; }
    await blockMutation.mutateAsync(friend.userId);
  };
  const unblock = async (friendId: number) => {
    if (previewMode) return;
    await unblockMutation.mutateAsync(friendId);
  };

  return {
    contacts,
    requests,
    isLoading: !previewMode && (friendsQuery.isLoading || requestsQuery.isLoading),
    error: friendsQuery.error ?? requestsQuery.error,
    isMutating: acceptMutation.isPending || rejectMutation.isPending || sendMutation.isPending || removeMutation.isPending || blockMutation.isPending || unblockMutation.isPending,
    accept,
    reject,
    sendRequest,
    remove,
    block,
    unblock,
  };
}
