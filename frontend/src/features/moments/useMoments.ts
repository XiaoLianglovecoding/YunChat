import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import type { Moment, MomentLiker } from "../../../goim-api-types";
import { momentPosts } from "../../mocks/data";
import { momentsApi } from "../../lib/api";
import { useAuthStore } from "../../stores/authStore";

export interface MomentCommentView { id: number; author: string; content: string; mine?: boolean; }
export interface MomentView extends Omit<Moment, "comments"> { author: string; avatar?: string; time: string; comments: MomentCommentView[]; }

const initialPreview: MomentView[] = momentPosts.map((post) => ({
  id: post.id,
  author_id: 2000 + post.id,
  author: post.author,
  author_name: post.author,
  content: post.content,
  visibility: 2,
  created_at: new Date(Date.now() - post.id * 3_600_000).toISOString(),
  like_count: post.likes.length,
  liked_by_me: post.id === 2,
  time: post.time,
  comments: post.comments.map((comment, index) => ({ id: post.id * 100 + index, ...comment })),
}));

function relativeTime(value: string) {
  const time = new Date(value).getTime();
  const minutes = Math.max(0, Math.floor((Date.now() - time) / 60_000));
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return `${minutes} 分钟前`;
  if (minutes < 1_440) return `${Math.floor(minutes / 60)} 小时前`;
  return new Intl.DateTimeFormat("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(time);
}

export function useMoments(mode: "friends" | "mine" = "friends") {
  const previewMode = useAuthStore((state) => state.previewMode);
  const currentUser = useAuthStore((state) => state.user);
  const queryClient = useQueryClient();
  const [previewPosts, setPreviewPosts] = useState(initialPreview);

  const feedQuery = useInfiniteQuery({
    queryKey: ["moments", "feed"],
    queryFn: ({ pageParam }) => momentsApi.feed(pageParam, 20),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.next_cursor || undefined,
    enabled: !previewMode && mode === "friends",
  });
  const mineQuery = useQuery({ queryKey: ["moments", "mine", currentUser?.id], queryFn: () => momentsApi.byUser(currentUser!.id, 100, 0), enabled: !previewMode && mode === "mine" && Boolean(currentUser?.id) });
  const publishMutation = useMutation({ mutationFn: (input: { content: string; visibility: 2 | 3 }) => momentsApi.publish(input), onSuccess: () => { void queryClient.invalidateQueries({ queryKey: ["moments", "feed"] }); void queryClient.invalidateQueries({ queryKey: ["moments", "mine"] }); } });
  const likeMutation = useMutation({ mutationFn: ({ id, liked }: { id: number; liked: boolean }) => liked ? momentsApi.unlike(id) : momentsApi.like(id), onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["moments", "feed"] }) });
  const commentMutation = useMutation({ mutationFn: ({ id, content }: { id: number; content: string }) => momentsApi.comment(id, { content }) });
  const deleteCommentMutation = useMutation({ mutationFn: (commentId: number) => momentsApi.deleteComment(commentId) });
  const deleteMomentMutation = useMutation({ mutationFn: (momentId: number) => momentsApi.delete(momentId), onSuccess: () => { void queryClient.invalidateQueries({ queryKey: ["moments", "feed"] }); void queryClient.invalidateQueries({ queryKey: ["moments", "mine"] }); } });

  const posts: MomentView[] = useMemo(() => {
    if (previewMode) return previewPosts;
    const source = mode === "mine" ? (mineQuery.data?.items ?? []) : (feedQuery.data?.pages.flatMap((page) => page.moments) ?? []);
    return source.map((post) => ({ ...post, author: post.author_name || (post.author_id === currentUser?.id ? currentUser.username : `用户 #${post.author_id}`), avatar: post.author_avatar, time: relativeTime(post.created_at), comments: (post.comments ?? []).map((comment) => ({ id: comment.id, author: comment.username || `用户 #${comment.user_id}`, content: comment.content, mine: comment.user_id === currentUser?.id })) }));
  }, [currentUser, feedQuery.data, mineQuery.data, mode, previewMode, previewPosts]);

  const publish = async (content: string, visibility: 2 | 3) => {
    if (previewMode) {
      setPreviewPosts((current) => [{ id: Date.now(), author_id: currentUser?.id ?? 10086, author_name: currentUser?.username ?? "我", author_avatar: currentUser?.avatarUrl, author: currentUser?.username ?? "我", avatar: currentUser?.avatarUrl, content, visibility, created_at: new Date().toISOString(), like_count: 0, liked_by_me: false, time: "刚刚", comments: [] }, ...current]);
      return;
    }
    await publishMutation.mutateAsync({ content, visibility });
  };
  const toggleLike = async (post: MomentView) => {
    if (previewMode) { setPreviewPosts((current) => current.map((item) => item.id === post.id ? { ...item, liked_by_me: !item.liked_by_me, like_count: Math.max(0, item.like_count + (item.liked_by_me ? -1 : 1)) } : item)); return; }
    await likeMutation.mutateAsync({ id: post.id, liked: post.liked_by_me });
  };
  const addComment = async (postId: number, content: string) => {
    const commentId = previewMode ? Date.now() : (await commentMutation.mutateAsync({ id: postId, content })).comment_id;
    const comment = { id: commentId, author: currentUser?.username ?? "我", content, mine: true };
    if (previewMode) setPreviewPosts((current) => current.map((post) => post.id === postId ? { ...post, comments: [...post.comments, comment] } : post));
    else { void queryClient.invalidateQueries({ queryKey: ["moments", "feed"] }); void queryClient.invalidateQueries({ queryKey: ["moments", "mine"] }); }
  };
  const deleteComment = async (postId: number, commentId: number) => {
    if (!previewMode) await deleteCommentMutation.mutateAsync(commentId);
    if (previewMode) setPreviewPosts((current) => current.map((post) => post.id === postId ? { ...post, comments: post.comments.filter((comment) => comment.id !== commentId) } : post));
    else { void queryClient.invalidateQueries({ queryKey: ["moments", "feed"] }); void queryClient.invalidateQueries({ queryKey: ["moments", "mine"] }); }
  };
  const getLikers = async (post: MomentView): Promise<MomentLiker[]> => {
    if (previewMode) return post.liked_by_me ? [{ user_id: currentUser?.id ?? 10086, username: currentUser?.username ?? "我", avatar_url: currentUser?.avatarUrl }] : [];
    return (await momentsApi.likers(post.id)).items;
  };
  const deleteMoment = async (postId: number) => {
    if (!previewMode) await deleteMomentMutation.mutateAsync(postId);
    else setPreviewPosts((current) => current.filter((post) => post.id !== postId));
  };

  return { posts, publish, toggleLike, addComment, deleteComment, getLikers, deleteMoment, isLoading: !previewMode && (mode === "mine" ? mineQuery.isLoading : feedQuery.isLoading), isError: !previewMode && (mode === "mine" ? mineQuery.isError : feedQuery.isError), hasNextPage: mode === "friends" && feedQuery.hasNextPage, fetchNextPage: feedQuery.fetchNextPage, isFetchingNextPage: feedQuery.isFetchingNextPage, isMutating: publishMutation.isPending || likeMutation.isPending || commentMutation.isPending || deleteCommentMutation.isPending };
}
