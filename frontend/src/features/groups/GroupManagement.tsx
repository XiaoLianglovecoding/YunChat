import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Crown, LogOut, ShieldCheck, UserPlus, Users, Volume2, VolumeX, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { Group, GroupMember } from "../../../goim-api-types";
import { ApiError } from "../../api/client";
import { Avatar, Button, ConfirmDialog, Drawer, IconButton, Switch, TextField } from "../../components/ui";
import { refreshGroupConversations } from "../../components/realtime/RealtimeBootstrap";
import { groupsApi } from "../../lib/api";
import { friendsApi } from "../../lib/api";
import { settingsApi } from "../../lib/api";
import { useAuthStore } from "../../stores/authStore";
import { useChatStore, type ChatConversation } from "../../stores/chatStore";
import { fetchAllGroupMembers, groupMembersQueryKey, type AllGroupMembers } from "./groupMembers";

const previewMembers: GroupMember[] = [
  { id: 1, group_id: 3001, user_id: 10086, role: 2, username: "顾言", joined_at: new Date().toISOString() },
  { id: 2, group_id: 3001, user_id: 2001, role: 1, username: "林澄", joined_at: new Date().toISOString() },
  { id: 3, group_id: 3001, user_id: 2002, role: 0, username: "周屿", joined_at: new Date().toISOString() },
  { id: 4, group_id: 3001, user_id: 2003, role: 0, username: "陈曦", joined_at: new Date().toISOString() },
];

const roleNames = { 0: "成员", 1: "管理员", 2: "群主" } as const;

const muteDurations = [
  { label: "10 分钟", minutes: 10 },
  { label: "1 小时", minutes: 60 },
  { label: "24 小时", minutes: 24 * 60 },
] as const;

const groupErrorMessages: Partial<Record<number, string>> = {
  1002: "提交的数据不合法，请检查后重试",
  1104: "这位用户不存在或已注销",
  1301: "你无权操作该成员",
  1302: "这个群聊不存在或已被解散",
  1303: "这位好友已经是群成员",
  1304: "群成员已达上限",
  1305: "不能移除群主，请先转让群主身份",
  1306: "群主不能直接退出，请先转让群主身份",
  1307: "成员角色只能是普通成员或管理员",
  1308: "只能邀请你的好友加入群聊",
  1309: "管理员不能移除同级管理员",
  1310: "这位用户已经不在群聊中",
  5001: "你已经不在这个群聊中",
};

function groupErrorMessage(failure: unknown, fallback: string) {
  if (failure instanceof ApiError) return groupErrorMessages[failure.code] ?? failure.message;
  if (failure instanceof Error && failure.message) return failure.message;
  return fallback;
}

export function canManageGroupProfile(group: Group | undefined, currentMember: GroupMember | undefined, currentUserId: number) {
  return group?.owner_id === currentUserId || currentMember?.role === 1;
}

export function canRemoveGroupMember(group: Group | undefined, currentMember: GroupMember | undefined, currentUserId: number, target: GroupMember) {
  if (!group || target.user_id === currentUserId || target.user_id === group.owner_id) return false;
  if (group.owner_id === currentUserId) return true;
  return currentMember?.role === 1 && target.role === 0;
}

export function canUpdateGroupMemberRole(group: Group | undefined, currentUserId: number, target: GroupMember) {
  return Boolean(group && group.owner_id === currentUserId && target.user_id !== group.owner_id && target.role !== 2);
}

export function canMuteGroupMember(group: Group | undefined, currentMember: GroupMember | undefined, currentUserId: number, target: GroupMember) {
  if (!group || target.user_id === currentUserId || target.user_id === group.owner_id) return false;
  if (group.owner_id === currentUserId) return target.role === 0 || target.role === 1;
  return currentMember?.role === 1 && target.role === 0;
}

export function canTransferGroupOwnership(group: Group | undefined, currentUserId: number, target: GroupMember) {
  return Boolean(
    group
    && currentUserId > 0
    && group.owner_id === currentUserId
    && target.group_id === group.id
    && target.user_id > 0
    && target.user_id !== group.owner_id,
  );
}

export function canLeaveGroup(group: Group | undefined, currentMember: GroupMember | undefined, currentUserId: number) {
  return Boolean(
    group
    && currentMember
    && currentUserId > 0
    && currentMember.group_id === group.id
    && currentMember.user_id === currentUserId
    && group.owner_id !== currentUserId,
  );
}

function membersAfterOwnershipTransfer(members: GroupMember[], previousOwnerId: number, newOwnerId: number) {
  return members.map((member) => {
    // 群主不能处于禁言状态；后端事务也会在升级新群主时清空该字段。
    if (member.user_id === newOwnerId) return { ...member, role: 2 as const, muted_until: undefined };
    if (member.user_id === previousOwnerId) return { ...member, role: 0 as const };
    return member;
  });
}

export function isGroupMemberMuted(member: GroupMember, now = Date.now()) {
  if (!member.muted_until) return false;
  const deadline = Date.parse(member.muted_until);
  return Number.isFinite(deadline) && deadline > now;
}

export function describeGroupMemberMute(member: GroupMember, now = Date.now()) {
  if (!member.muted_until) return null;
  const deadline = Date.parse(member.muted_until);
  if (!Number.isFinite(deadline)) return "禁言时间异常";
  const formatted = new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(deadline);
  return deadline > now ? `禁言至 ${formatted}` : `禁言已于 ${formatted} 到期`;
}

interface CreateGroupDrawerProps {
  open: boolean;
  onClose: () => void;
  onCreated: (conversationId: string) => void;
}

export function CreateGroupDrawer({ open, onClose, onCreated }: CreateGroupDrawerProps) {
  const previewMode = useAuthStore((state) => state.previewMode);
  const addGroupConversation = useChatStore((state) => state.addGroupConversation);
  const [name, setName] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState<string | null>(null);
  const createMutation = useMutation({ mutationFn: () => groupsApi.create({ name: name.trim(), notice: notice.trim() }) });

  const createGroup = async () => {
    if (!name.trim()) return;
    setError(null);
    try {
      const groupId = previewMode ? Date.now() % 1_000_000 : (await createMutation.mutateAsync()).group_id;
      addGroupConversation(groupId, name.trim());
      const id = previewMode ? `preview-group-${groupId}` : `g_${groupId}`;
      setName(""); setNotice(""); onClose(); onCreated(id);
    } catch (failure) {
      setError(failure instanceof ApiError ? failure.message : "创建群聊失败");
    }
  };

  return <Drawer description="创建后你将自动成为群主。" onClose={onClose} open={open} title="创建群聊"><div className="group-form"><TextField label="群名称" maxLength={50} onChange={(event) => setName(event.target.value)} placeholder="例如：项目讨论组" value={name} /><label className="ui-field"><span className="ui-field__label">群公告（选填）</span><span className="ui-field__control group-textarea"><textarea maxLength={300} onChange={(event) => setNotice(event.target.value)} placeholder="介绍这个群聊的用途" rows={4} value={notice} /></span></label>{error && <p className="inline-error">{error}</p>}<Button disabled={!name.trim() || createMutation.isPending} loading={createMutation.isPending} onClick={() => void createGroup()} size="lg">创建群聊</Button></div></Drawer>;
}

interface GroupManagementDrawerProps {
  conversation: ChatConversation;
  open: boolean;
  onClose: () => void;
}

export function GroupManagementDrawer({ conversation, open, onClose }: GroupManagementDrawerProps) {
  const previewMode = useAuthStore((state) => state.previewMode);
  const currentUserId = useAuthStore((state) => state.user?.id ?? 0);
  const queryClient = useQueryClient();
  const removeConversation = useChatStore((state) => state.removeConversation);
  const setConversationIdentity = useChatStore((state) => state.setConversationIdentity);
  const setConversationMuted = useChatStore((state) => state.setConversationMuted);
  const groupId = conversation.targetId;
  const [localGroup, setLocalGroup] = useState<Group>({ id: groupId, name: conversation.name, notice: "保持信息透明，重要结论及时同步。", owner_id: currentUserId, max_members: 500, created_at: new Date().toISOString(), updated_at: new Date().toISOString() });
  const [localMembers, setLocalMembers] = useState(previewMembers.map((member) => ({ ...member, group_id: groupId })));
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(conversation.name);
  const [notice, setNotice] = useState(localGroup.notice);
  const [danger, setDanger] = useState<{ type: "remove" | "leave" | "transfer" | "role"; memberId?: number; nextRole?: 0 | 1 } | null>(null);
  const [muteTargetId, setMuteTargetId] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [muteSaving, setMuteSaving] = useState(false);
  const [clockNow, setClockNow] = useState(() => Date.now());
  const actionRunningRef = useRef(false);
  const groupQuery = useQuery({ queryKey: ["group", groupId], queryFn: () => groupsApi.get(groupId), enabled: open && !previewMode });
  const membersQuery = useQuery({ queryKey: groupMembersQueryKey(groupId), queryFn: () => fetchAllGroupMembers(groupId), enabled: open && !previewMode });
  const actionMutation = useMutation({ mutationFn: async (action: () => Promise<unknown>) => action() });
  const group = previewMode ? localGroup : groupQuery.data;
  const members = previewMode ? localMembers : (membersQuery.data?.items ?? []);
  const memberTotal = previewMode ? localMembers.length : membersQuery.data?.total;
  const currentMember = members.find((member) => member.user_id === currentUserId);
  const isOwner = group?.owner_id === currentUserId;
  const canManage = isOwner || currentMember?.role === 1;
  const canEditProfile = canManageGroupProfile(group, currentMember, currentUserId);
  const isFull = memberTotal !== undefined && memberTotal >= (group?.max_members ?? 500);
  const friendsQuery = useQuery({ queryKey: ["friends"], queryFn: () => friendsApi.list(100, 0), enabled: open && !previewMode && canManage });

  const nearestActiveMuteDeadline = useMemo(() => members.reduce<number | null>((nearest, member) => {
    if (!member.muted_until) return nearest;
    const deadline = Date.parse(member.muted_until);
    if (!Number.isFinite(deadline) || deadline <= clockNow) return nearest;
    return nearest === null || deadline < nearest ? deadline : nearest;
  }, null), [clockNow, members]);

  useEffect(() => {
    if (open) setClockNow(Date.now());
  }, [open]);

  useEffect(() => {
    if (!open || nearestActiveMuteDeadline === null) return;
    const delay = Math.min(Math.max(nearestActiveMuteDeadline - Date.now() + 50, 0), 2_147_483_647);
    const timer = window.setTimeout(() => setClockNow(Date.now()), delay);
    return () => window.clearTimeout(timer);
  }, [clockNow, nearestActiveMuteDeadline, open]);

  useEffect(() => {
    if (!groupQuery.data) return;
    setName(groupQuery.data.name); setNotice(groupQuery.data.notice);
  }, [groupQuery.data]);

  const revalidateGroupState = () => {
    void queryClient.invalidateQueries({ queryKey: ["group", groupId] });
    void queryClient.invalidateQueries({ queryKey: groupMembersQueryKey(groupId) });
  };

  const run = async (
    previewAction: () => void,
    liveAction: () => Promise<unknown>,
    revalidateAfterSuccess = true,
    acceptedErrorCodes: number[] = [],
  ) => {
    if (actionRunningRef.current) return false;
    actionRunningRef.current = true;
    setError(null);
    try {
      previewMode ? previewAction() : await actionMutation.mutateAsync(liveAction);
      if (!previewMode && revalidateAfterSuccess) revalidateGroupState();
      return true;
    }
    catch (failure) {
      // Desired-state commands may be retried after their first successful
      // response was lost. "Already absent" then means the goal was reached.
      if (failure instanceof ApiError && acceptedErrorCodes.includes(failure.code)) return true;
      if (failure instanceof ApiError && [1301, 1303, 1306, 1310, 5001].includes(failure.code)) {
        void queryClient.invalidateQueries({ queryKey: ["group", groupId] });
        void queryClient.invalidateQueries({ queryKey: groupMembersQueryKey(groupId) });
      }
      setError(groupErrorMessage(failure, "操作失败，请稍后重试"));
      return false;
    } finally {
      actionRunningRef.current = false;
    }
  };

  const saveInfo = () => {
    const nextName = name.trim();
    const nextNotice = notice.trim();
    void run(
      () => setLocalGroup((current) => ({ ...current, name: nextName, notice: nextNotice })),
      () => groupsApi.update(groupId, { name: nextName, notice: nextNotice }),
    ).then((succeeded) => {
      if (!succeeded) return;
      setConversationIdentity(conversation.id, nextName);
      setEditing(false);
    });
  };
  const toggleMute = async (muted: boolean) => {
    if (muteSaving) return;
    setError(null);
    setMuteSaving(true);
    try {
      if (!previewMode) {
        if (muted) await settingsApi.mute({ convId: conversation.id });
        else await settingsApi.unmute(conversation.id);
      }
      setConversationMuted(conversation.id, muted);
    } catch (failure) {
      setError(groupErrorMessage(failure, "设置免打扰失败，请稍后重试"));
    } finally {
      setMuteSaving(false);
    }
  };
  const addMember = (memberId: number, username: string, avatarUrl?: string) => { void run(() => setLocalMembers((current) => [...current, { id: Date.now(), group_id: groupId, user_id: memberId, role: 0, username, avatar_url: avatarUrl, joined_at: new Date().toISOString() }]), () => groupsApi.addMember(groupId, { member_id: memberId })); };
  const updateRole = (memberId: number, role: 0 | 1) => run(
    () => setLocalMembers((current) => current.map((item) => item.user_id === memberId ? { ...item, role } : item)),
    () => groupsApi.updateRole(groupId, memberId, { role }),
  );
  const muteMember = (member: GroupMember, minutes: number) => {
    const mutedUntil = new Date(Date.now() + minutes * 60_000).toISOString();
    void run(
      () => setLocalMembers((current) => current.map((item) => item.user_id === member.user_id ? { ...item, muted_until: mutedUntil } : item)),
      () => groupsApi.muteMember(groupId, member.user_id, { muted_until: mutedUntil }),
    ).then((succeeded) => { if (succeeded) setMuteTargetId(null); });
  };
  const unmuteMember = (member: GroupMember) => {
    void run(
      () => setLocalMembers((current) => current.map((item) => item.user_id === member.user_id ? { ...item, muted_until: null } : item)),
      () => groupsApi.unmuteMember(groupId, member.user_id),
    );
  };
  const confirmDanger = async () => {
    if (!danger) return;
    let succeeded = false;
    if (danger.type === "remove" && danger.memberId) succeeded = await run(() => setLocalMembers((current) => current.filter((member) => member.user_id !== danger.memberId)), () => groupsApi.removeMember(groupId, danger.memberId!));
    if (danger.type === "leave") {
      if (!canLeaveGroup(group, currentMember, currentUserId)) {
        setDanger(null);
        setError(isOwner ? groupErrorMessages[1306]! : "你已经不在这个群聊中");
        return;
      }
      succeeded = await run(() => undefined, () => groupsApi.leave(groupId), false, [1302, 5001]);
    }
    if (danger.type === "transfer" && danger.memberId) {
      const target = members.find((member) => member.user_id === danger.memberId);
      if (!target || !canTransferGroupOwnership(group, currentUserId, target)) {
        setDanger(null);
        setError("群主身份或目标成员已发生变化，请刷新后重试");
        revalidateGroupState();
        return;
      }
      const newOwnerId = target.user_id;
      succeeded = await run(
        () => {
          setLocalGroup((current) => ({ ...current, owner_id: newOwnerId, updated_at: new Date().toISOString() }));
          setLocalMembers((current) => membersAfterOwnershipTransfer(current, currentUserId, newOwnerId));
        },
        () => groupsApi.transferOwner(groupId, { new_owner_id: newOwnerId }),
        false,
      );
      if (succeeded && !previewMode) {
        queryClient.setQueryData<Group>(["group", groupId], (current) => current ? {
          ...current,
          owner_id: newOwnerId,
          updated_at: new Date().toISOString(),
        } : current);
        queryClient.setQueryData<AllGroupMembers>(groupMembersQueryKey(groupId), (current) => current ? {
          ...current,
          items: membersAfterOwnershipTransfer(current.items, currentUserId, newOwnerId),
        } : current);
        revalidateGroupState();
      }
    }
    if (danger.type === "role" && danger.memberId && danger.nextRole !== undefined) succeeded = await updateRole(danger.memberId, danger.nextRole);
    if (!succeeded) return;
    setDanger(null);
    if (danger.type === "leave") {
      // 先让宿主卸载当前抽屉里的活跃 Query observer，再删除对应缓存，
      // 否则 observer 可能在 removeQueries 后立刻把同一个 Query 建回来。
      removeConversation(conversation.id);
      onClose();
      if (!previewMode) {
        // Calling now increments the refresh generation synchronously, before
        // any older list response gets a chance to recreate this conversation.
        const authoritativeRefresh = refreshGroupConversations(currentUserId);
        await Promise.all([
          queryClient.cancelQueries({ queryKey: ["group", groupId], exact: true }),
          queryClient.cancelQueries({ queryKey: groupMembersQueryKey(groupId), exact: true }),
        ]);
        queryClient.removeQueries({ queryKey: ["group", groupId], exact: true });
        queryClient.removeQueries({ queryKey: groupMembersQueryKey(groupId), exact: true });
        // This also supersedes any older in-flight group-list response that
        // could otherwise recreate the conversation after local cleanup.
        void authoritativeRefresh;
      }
    }
  };

  const sortedMembers = useMemo(() => [...members].sort((a, b) => b.role - a.role), [members]);
  const muteTarget = members.find((member) => member.user_id === muteTargetId);
  const dangerTarget = members.find((member) => member.user_id === danger?.memberId);
  const memberIds = useMemo(() => new Set(members.map((member) => member.user_id)), [members]);
  const inviteCandidates = (friendsQuery.data?.items ?? []).filter((friend) => !memberIds.has(friend.friend_id) && !friend.is_blocked);
  const memberSummary = memberTotal !== undefined ? `${memberTotal} 位成员` : membersQuery.isError ? "成员加载失败" : "成员加载中";
  const confirmationLabel = danger?.type === "leave" ? "退出群聊" : danger?.type === "transfer" ? "转让群主" : danger?.type === "role" ? (danger.nextRole === 1 ? "设为管理员" : "取消管理员") : "移除成员";
  const confirmationDescription = danger?.type === "leave" ? "退出后，本地会话和消息会被移除，你也将不再接收这个群的消息。" : danger?.type === "transfer" ? `转让后，${dangerTarget?.username || `用户 #${danger.memberId ?? ""}`} 将成为新群主，你将变为普通成员。` : danger?.type === "role" ? (danger.nextRole === 1 ? `用户 #${danger.memberId ?? ""} 将获得群管理权限。` : `用户 #${danger.memberId ?? ""} 将失去群管理权限。`) : `确认将用户 #${danger?.memberId ?? ""} 移出群聊？`;
  const confirmationTitle = danger?.type === "leave" ? "确定退出群聊？" : danger?.type === "transfer" ? "转让群主？" : danger?.type === "role" ? (danger.nextRole === 1 ? "设为管理员？" : "取消管理员？") : "移除这位成员？";

  return (
    <>
      <Drawer description={`${memberSummary} · 最多 ${group?.max_members ?? 500} 人`} onClose={onClose} open={open} title="群聊资料">
        <div className="group-profile-head">
          <Avatar name={group?.name ?? conversation.name} size="xl" />
          <div><h3>{group?.name ?? conversation.name}</h3><p>{group?.notice || "暂无群公告"}</p></div>
          {canEditProfile && <Button disabled={actionMutation.isPending} onClick={() => setEditing((value) => !value)} size="sm" variant="secondary">{editing ? "取消编辑" : "编辑资料"}</Button>}
        </div>
        <div className="group-notification-setting"><Switch checked={Boolean(conversation.muted)} description="开启后，该群聊不会触发声音与桌面通知。" disabled={muteSaving} label="消息免打扰" onCheckedChange={(checked) => void toggleMute(checked)} /></div>
        {error && <p className="inline-error">{error}</p>}
        {editing && <div className="group-edit-panel"><TextField label="群名称" onChange={(event) => setName(event.target.value)} value={name} /><TextField label="群公告" onChange={(event) => setNotice(event.target.value)} value={notice} /><Button disabled={!name.trim() || actionMutation.isPending} loading={actionMutation.isPending} onClick={saveInfo} size="sm">保存更改</Button></div>}

        {canManage && (
          <div className="group-add-member">
            <h3>从好友中邀请</h3>
            {isFull ? <p>群成员已达上限，暂时不能继续邀请。</p>
              : !previewMode && friendsQuery.isLoading ? <p>正在加载好友列表…</p>
                : !previewMode && friendsQuery.isError ? <div className="group-query-state"><p>{groupErrorMessage(friendsQuery.error, "加载好友列表失败")}</p><Button onClick={() => void friendsQuery.refetch()} size="sm" variant="secondary">重试</Button></div>
                  : inviteCandidates.length === 0 ? <p>暂无可邀请的好友</p>
                    : inviteCandidates.map((friend) => <div className="group-invite-row" key={friend.friend_id}><Avatar name={friend.nickname || `用户 ${friend.friend_id}`} size="sm" src={friend.avatar_url} /><span><strong>{friend.nickname || `用户 #${friend.friend_id}`}</strong><small>用户 #{friend.friend_id}</small></span><Button disabled={actionMutation.isPending || isFull} leadingIcon={<UserPlus size={14} />} onClick={() => addMember(friend.friend_id, friend.nickname || `用户 ${friend.friend_id}`, friend.avatar_url)} size="sm">添加</Button></div>)}
          </div>
        )}

        <section className="group-members">
          <header><h3><Users size={16} />群成员</h3><span>{memberTotal ?? "—"}</span></header>
          {muteTarget && canMuteGroupMember(group, currentMember, currentUserId, muteTarget) && (
            <div aria-label="选择禁言时长" className="group-mute-panel" role="group">
              <header>
                <span><strong>禁言 {muteTarget.username || `用户 #${muteTarget.user_id}`}</strong><small>到期后会自动恢复发言</small></span>
                <IconButton disabled={actionMutation.isPending} label="取消禁言设置" onClick={() => setMuteTargetId(null)}><X size={14} /></IconButton>
              </header>
              <div>
                {muteDurations.map((duration) => (
                  <Button disabled={actionMutation.isPending} key={duration.minutes} onClick={() => muteMember(muteTarget, duration.minutes)} size="sm" variant="secondary">
                    {duration.label}
                  </Button>
                ))}
              </div>
            </div>
          )}
          {!previewMode && membersQuery.isLoading && <div className="group-query-state"><p>正在加载群成员…</p></div>}
          {!previewMode && membersQuery.isError && <div className="group-query-state"><p>{groupErrorMessage(membersQuery.error, "加载群成员失败")}</p><Button onClick={() => void membersQuery.refetch()} size="sm" variant="secondary">重试</Button></div>}
          {!membersQuery.isLoading && !membersQuery.isError && sortedMembers.length === 0 && <div className="group-query-state"><p>暂无群成员</p></div>}
          {sortedMembers.map((member) => (
            <div className="group-member-row" key={member.user_id}>
              <Avatar name={member.username || `用户 ${member.user_id}`} size="sm" src={member.avatar_url} />
              <div className="group-member-copy">
                <strong>{member.user_id === currentUserId ? `${member.username || "我"}（我）` : member.username || `用户 #${member.user_id}`}</strong>
                <small>用户 #{member.user_id} · {roleNames[member.role]}</small>
                {describeGroupMemberMute(member, clockNow) && <small className={isGroupMemberMuted(member, clockNow) ? "is-muted" : undefined}><VolumeX size={10} />{describeGroupMemberMute(member, clockNow)}</small>}
              </div>
              <span className={`role-badge role-badge--${member.role}`}>{member.role === 2 ? <Crown size={11} /> : member.role === 1 ? <ShieldCheck size={11} /> : null}{roleNames[member.role]}</span>
              <div className="group-member-actions">
                {canUpdateGroupMemberRole(group, currentUserId, member) && <IconButton disabled={actionMutation.isPending} label={member.role === 1 ? "取消管理员" : "设为管理员"} onClick={() => setDanger({ type: "role", memberId: member.user_id, nextRole: member.role === 1 ? 0 : 1 })}><ShieldCheck size={15} /></IconButton>}
                {canTransferGroupOwnership(group, currentUserId, member) && <IconButton disabled={actionMutation.isPending} label="转让群主" onClick={() => setDanger({ type: "transfer", memberId: member.user_id })}><Crown size={15} /></IconButton>}
                {canMuteGroupMember(group, currentMember, currentUserId, member) && (isGroupMemberMuted(member, clockNow)
                  ? <IconButton disabled={actionMutation.isPending} label="解除禁言" onClick={() => unmuteMember(member)} selected><Volume2 size={15} /></IconButton>
                  : <IconButton disabled={actionMutation.isPending} label="禁言成员" onClick={() => setMuteTargetId(member.user_id)}><VolumeX size={15} /></IconButton>)}
                {canRemoveGroupMember(group, currentMember, currentUserId, member) && <IconButton disabled={actionMutation.isPending} label="移除成员" onClick={() => setDanger({ type: "remove", memberId: member.user_id })}><X size={15} /></IconButton>}
              </div>
            </div>
          ))}
        </section>
        {isOwner ? <p className="group-query-state">群主需先转让身份才能退出群聊。</p> : canLeaveGroup(group, currentMember, currentUserId) ? <Button disabled={actionMutation.isPending} leadingIcon={<LogOut size={15} />} onClick={() => setDanger({ type: "leave" })} variant="danger">退出群聊</Button> : null}
      </Drawer>
      <ConfirmDialog confirmLabel={confirmationLabel} confirming={actionMutation.isPending} description={confirmationDescription} destructive={danger?.type === "leave" || danger?.type === "remove"} onClose={() => { if (!actionMutation.isPending) setDanger(null); }} onConfirm={() => void confirmDanger()} open={Boolean(danger)} title={confirmationTitle} />
    </>
  );
}
