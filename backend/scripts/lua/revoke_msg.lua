-- TODO[MSG-005]: 校验发送者与撤回时限，并原子替换 inbox/outbox 中的消息。
-- 实现前不要加载到生产 Redis。
return redis.error_reply('TODO[MSG-005]: revoke_msg')
