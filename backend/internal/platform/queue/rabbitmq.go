package queue

import (
	"context"
	"fmt"

	"github.com/XiaoLianglovecoding/linknest-im/backend/internal/config"
	"github.com/rabbitmq/amqp091-go"
)

type RabbitMQ struct {
	connection *amqp091.Connection
	channel    *amqp091.Channel
	exchange   string
}

func OpenRabbitMQ(cfg config.RabbitMQ) (*RabbitMQ, error) {
	connection, err := amqp091.Dial(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}
	channel, err := connection.Channel()
	if err != nil {
		connection.Close()
		return nil, fmt.Errorf("open rabbitmq channel: %w", err)
	}
	if err := channel.ExchangeDeclare(cfg.Exchange, "topic", true, false, false, false, nil); err != nil {
		channel.Close()
		connection.Close()
		return nil, fmt.Errorf("declare rabbitmq exchange: %w", err)
	}
	if err := channel.Confirm(false); err != nil {
		channel.Close()
		connection.Close()
		return nil, fmt.Errorf("enable rabbitmq confirms: %w", err)
	}
	return &RabbitMQ{connection: connection, channel: channel, exchange: cfg.Exchange}, nil
}

func (mq *RabbitMQ) Publish(ctx context.Context, routingKey, eventID string, body []byte) error {
	confirmation, err := mq.channel.PublishWithDeferredConfirmWithContext(
		ctx,
		mq.exchange,
		routingKey,
		false,
		false,
		amqp091.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp091.Persistent,
			MessageId:    eventID,
			Body:         body,
		},
	)
	if err != nil {
		return err
	}
	confirmed, err := confirmation.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("rabbitmq did not confirm event %s", eventID)
	}
	return nil
}

func (mq *RabbitMQ) Close() error {
	channelErr := mq.channel.Close()
	connectionErr := mq.connection.Close()
	if channelErr != nil {
		return channelErr
	}
	return connectionErr
}
