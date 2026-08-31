package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"my-im/internal/consumer"
	"my-im/internal/infra"
	"my-im/internal/model"
)

type RabbitPublisher struct{ client *infra.RabbitMQ }

func NewRabbitPublisher(client *infra.RabbitMQ) *RabbitPublisher {
	return &RabbitPublisher{client: client}
}

func publishJSON(ctx context.Context, client *infra.RabbitMQ, queue string, value any, messageID string) error {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s event: %w", queue, err)
	}
	return client.Publish(ctx, queue, body, messageID)
}
func (p *RabbitPublisher) PublishPrivateMessage(ctx context.Context, msg *model.PrivateMessage) error {
	return publishJSON(ctx, p.client, consumer.PrivateMessageQueue, msg, strconv.FormatInt(msg.ID, 10))
}
func (p *RabbitPublisher) PublishGroupMessage(ctx context.Context, msg *model.GroupMessage) error {
	return publishJSON(ctx, p.client, consumer.GroupMessageQueue, msg, strconv.FormatInt(msg.ID, 10))
}
func (p *RabbitPublisher) PublishMomentPush(ctx context.Context, moment *model.Moment) error {
	return publishJSON(ctx, p.client, consumer.MomentPushQueue, moment, strconv.FormatInt(moment.ID, 10))
}
func (p *RabbitPublisher) PublishLikeEvent(ctx context.Context, event *model.LikeEvent) error {
	return publishJSON(ctx, p.client, consumer.LikePersistQueue, event, fmt.Sprintf("%d:%d:%d", event.MomentID, event.UserID, event.Ts))
}

var _ MessagePublisher = (*RabbitPublisher)(nil)
