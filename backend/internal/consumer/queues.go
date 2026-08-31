// Package consumer 保存 RabbitMQ 消费者。当前仅固化队列契约。
package consumer

const (
	PrivateMessageQueue = "private_msg_persist" // TODO[MQ-001]
	GroupMessageQueue   = "group_msg_fanout"    // TODO[MQ-002]
	MomentPushQueue     = "moment_push"         // TODO[MOMENT-002]
	LikePersistQueue    = "like_persist"        // TODO[MOMENT-003]
	CommentPersistQueue = "comment_persist"     // TODO[MOMENT-004]: 原项目只声明队列，尚无生产/消费实现。
)
