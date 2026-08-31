// Package consumer 保存 RabbitMQ 消费者。当前仅固化队列契约。
package consumer

const (
	PrivateMessageQueue = "private_msg_persist" // TODO[MQ-001]
	GroupMessageQueue   = "group_msg_fanout"    // TODO[MQ-002]
	MomentPushQueue     = "moment_push"         // TODO[MOMENT-002]
	LikePersistQueue    = "like_persist"        // TODO[MOMENT-003]
)

// 原项目的 comment_persist 只有声明、没有生产者和消费者，容易造成“看起来已实现”的假象。
// MyIM 在 MOMENT-004 中采用同步写评论，因此基础设施明确不再声明该队列。
