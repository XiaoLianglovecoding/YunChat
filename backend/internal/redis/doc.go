// Package redis 将承载唯一权威的 Redis Lua 脚本及其类型安全执行封装。
//
// TODO[INFRA-002]: 选择 go:embed 或 Go 常量作为单一脚本来源。
// TODO[MSG-000]: 设计在目标吞吐下可证明唯一、可恢复的消息 ID 生成器。
// TODO[MSG-001]: 私聊原子检查。
// TODO[MSG-002]: 群聊原子检查。
// TODO[MSG-003]: 已读原子更新。
// TODO[MSG-005]: 撤回原子更新。
// TODO[MOMENT-003]: 点赞/取消点赞原子更新。
package redis
