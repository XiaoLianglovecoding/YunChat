package infra

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"my-im/internal/config"
	"my-im/internal/consumer"
)

const DeadLetterExchange = "my_im.dlx"

var DurableQueues = []string{
	consumer.PrivateMessageQueue,
	consumer.GroupMessageQueue,
	consumer.MomentPushQueue,
	consumer.LikePersistQueue,
}

type PublishObserver func(retries int, err error)

// RabbitMQ 串行保护一个 confirm channel。基础阶段优先保证发布语义正确；需要更高吞吐时可扩展为 channel pool。
type RabbitMQ struct {
	mu       sync.Mutex
	cfg      config.RabbitMQConfig
	conn     *amqp.Connection
	channel  *amqp.Channel
	confirms <-chan amqp.Confirmation
	returns  <-chan amqp.Return
	observer PublishObserver
	closed   bool
}

func OpenRabbitMQ(ctx context.Context, cfg config.RabbitMQConfig, observer PublishObserver) (*RabbitMQ, error) {
	r := &RabbitMQ{cfg: cfg, observer: observer}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.connectLocked(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RabbitMQ) connectLocked(ctx context.Context) error {
	if r.closed {
		return errors.New("rabbitmq client is closed")
	}
	if r.channel != nil {
		_ = r.channel.Close()
		r.channel = nil
	}
	if r.conn != nil {
		_ = r.conn.Close()
		r.conn = nil
	}
	timeout := config.Milliseconds(r.cfg.ConnectTimeoutMS)
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := amqp.DialConfig(r.cfg.URL, amqp.Config{Dial: func(network, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, addr)
	}})
	if err != nil {
		return fmt.Errorf("connect rabbitmq: %w", err)
	}
	channel, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("open rabbitmq channel: %w", err)
	}
	if err := channel.Confirm(false); err != nil {
		_ = channel.Close()
		_ = conn.Close()
		return fmt.Errorf("enable rabbitmq publisher confirms: %w", err)
	}
	if err := declareTopology(channel); err != nil {
		_ = channel.Close()
		_ = conn.Close()
		return err
	}
	r.conn, r.channel = conn, channel
	r.confirms = channel.NotifyPublish(make(chan amqp.Confirmation, 1))
	r.returns = channel.NotifyReturn(make(chan amqp.Return, 1))
	return nil
}

func declareTopology(channel *amqp.Channel) error {
	if err := channel.ExchangeDeclare(DeadLetterExchange, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare dead-letter exchange: %w", err)
	}
	for _, queue := range DurableQueues {
		dlq := queue + ".dlq"
		if _, err := channel.QueueDeclare(dlq, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare %s: %w", dlq, err)
		}
		if err := channel.QueueBind(dlq, queue, DeadLetterExchange, false, nil); err != nil {
			return fmt.Errorf("bind %s: %w", dlq, err)
		}
		args := amqp.Table{"x-dead-letter-exchange": DeadLetterExchange, "x-dead-letter-routing-key": queue}
		if _, err := channel.QueueDeclare(queue, true, false, false, false, args); err != nil {
			return fmt.Errorf("declare %s: %w", queue, err)
		}
	}
	return nil
}

func (r *RabbitMQ) Publish(ctx context.Context, queue string, body []byte, messageID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var finalErr error
	for attempt := 0; attempt < r.cfg.RetryCount; attempt++ {
		if r.channel == nil || r.channel.IsClosed() {
			if err := r.connectLocked(ctx); err != nil {
				finalErr = err
			} else {
				finalErr = nil
			}
		}
		if finalErr == nil {
			finalErr = r.publishOnceLocked(ctx, queue, body, messageID)
		}
		if finalErr == nil {
			if r.observer != nil {
				r.observer(attempt, nil)
			}
			return nil
		}
		if r.channel != nil && r.channel.IsClosed() {
			r.channel = nil
		}
		if attempt+1 < r.cfg.RetryCount {
			timer := time.NewTimer(config.Milliseconds(r.cfg.RetryBackoffMS) * time.Duration(attempt+1))
			select {
			case <-ctx.Done():
				timer.Stop()
				finalErr = ctx.Err()
				attempt = r.cfg.RetryCount
			case <-timer.C:
			}
		}
	}
	if r.observer != nil {
		r.observer(r.cfg.RetryCount-1, finalErr)
	}
	return fmt.Errorf("publish rabbitmq queue %s after %d attempts: %w", queue, r.cfg.RetryCount, finalErr)
}

func (r *RabbitMQ) publishOnceLocked(ctx context.Context, queue string, body []byte, messageID string) error {
	publishCtx, cancel := context.WithTimeout(ctx, config.Milliseconds(r.cfg.PublishTimeoutMS))
	defer cancel()
	err := r.channel.PublishWithContext(publishCtx, "", queue, true, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent, ContentType: "application/json", Body: body,
		MessageId: messageID, Timestamp: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	var returnedMessage *amqp.Return
	for {
		select {
		case returned, ok := <-r.returns:
			if !ok {
				return errors.New("rabbitmq return channel closed")
			}
			// mandatory 退回之后 Broker 仍会发送 Confirm。把两者都消费掉，
			// 避免把上一轮遗留的 ACK 错当成下一轮发布的 ACK。
			copy := returned
			returnedMessage = &copy
		case confirmation, ok := <-r.confirms:
			if !ok {
				return errors.New("rabbitmq confirm channel closed")
			}
			if !confirmation.Ack {
				return errors.New("rabbitmq publisher nack")
			}
			if returnedMessage == nil {
				// Return 与 Confirm 已同时进入各自缓冲区时 select 可能先选 Confirm，
				// 因此再做一次非阻塞排空。
				select {
				case returned := <-r.returns:
					copy := returned
					returnedMessage = &copy
				default:
				}
			}
			if returnedMessage != nil {
				return fmt.Errorf("rabbitmq mandatory return %d %s (routing_key=%s)", returnedMessage.ReplyCode, returnedMessage.ReplyText, returnedMessage.RoutingKey)
			}
			return nil
		case <-publishCtx.Done():
			if returnedMessage != nil {
				return fmt.Errorf("rabbitmq mandatory return %d %s (confirm timeout: %w)", returnedMessage.ReplyCode, returnedMessage.ReplyText, publishCtx.Err())
			}
			return publishCtx.Err()
		}
	}
}

func (r *RabbitMQ) Check(ctx context.Context, timeout time.Duration) error {
	r.mu.Lock()
	if r.closed || r.conn == nil || r.conn.IsClosed() || r.channel == nil || r.channel.IsClosed() {
		r.mu.Unlock()
		return errors.New("rabbitmq connection is closed")
	}
	healthChannel, err := r.conn.Channel()
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("open rabbitmq health channel: %w", err)
	}
	defer healthChannel.Close()
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := healthChannel.QueueInspect(consumer.PrivateMessageQueue); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("rabbitmq queue inspect: %w", err)
		}
		return nil
	case <-checkCtx.Done():
		_ = healthChannel.Close()
		return checkCtx.Err()
	}
}

func (r *RabbitMQ) QueueDepths() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	depths := make(map[string]int, len(DurableQueues))
	if r.channel == nil || r.channel.IsClosed() {
		return depths
	}
	for _, queue := range DurableQueues {
		if info, err := r.channel.QueueInspect(queue); err == nil {
			depths[queue] = info.Messages
		}
	}
	return depths
}

func (r *RabbitMQ) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	var errs []error
	if r.channel != nil && !r.channel.IsClosed() {
		if err := r.channel.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.conn != nil && !r.conn.IsClosed() {
		if err := r.conn.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
