-- TODO[MSG-003]: 原子清零会话未读数并更新私聊消息 readStatus。
-- 实现前不要加载到生产 Redis。
return redis.error_reply('TODO[MSG-003]: inbox_mark_read')
