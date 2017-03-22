package mqs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/iron-io/functions/api/models"
	mq_config "github.com/iron-io/iron_go3/config"
	ironmq "github.com/iron-io/iron_go3/mq"
	"time"
)

type assoc struct {
	msgId         string
	reservationId string
}

type IronMQ struct {
	queues []ironmq.Queue
	// Protects the map
	sync.Mutex
	// job id to {msgid, reservationid}
	msgAssoc map[string]*assoc
	// TODO Reserve timeout in seconds.
	reserveTimeout int
}

type IronMQConfig struct {
	Token       string `mapstructure:"token"`
	ProjectId   string `mapstructure:"project_id"`
	Host        string `mapstructure:"host"`
	Scheme      string `mapstructure:"scheme"`
	Port        uint16 `mapstructure:"port"`
	QueuePrefix string `mapstructure:"queue_prefix"`
}

func NewIronMQ(url *url.URL, reserveTimeout time.Duration) (*IronMQ, error) {

	if url.User == nil || url.User.Username() == "" {
		logrus.Fatal("IronMQ requires PROJECT_ID and TOKEN")
	}
	p, ok := url.User.Password()
	if !ok {
		logrus.Fatal("IronMQ requires PROJECT_ID and TOKEN")
	}
	settings := &mq_config.Settings{
		Token:     p,
		ProjectId: url.User.Username(),
		Host:      url.Host,
		Scheme:    "https",
	}

	if url.Scheme == "ironmq+http" {
		settings.Scheme = "http"
	}

	parts := strings.Split(url.Host, ":")
	if len(parts) > 1 {
		settings.Host = parts[0]
		p, err := strconv.Atoi(parts[1])
		if err != nil {
			logrus.WithFields(logrus.Fields{"host_port": url.Host}).Fatal("Invalid host+port combination")
		}
		settings.Port = uint16(p)
	}

	var queueName string
	if len(url.Path) > 1 {
		// Drop leading '/'.
		queueName = url.Path[1:]
	} else {
		queueName = "titan"
	}
	mq := &IronMQ{
		queues:   make([]ironmq.Queue, 3),
		msgAssoc: make(map[string]*assoc),
		reserveTimeout: int(reserveTimeout/time.Second),
	}

	// Check we can connect by trying to create one of the queues. Create is
	// idempotent, so this is fine.
	_, err := ironmq.ConfigCreateQueue(ironmq.QueueInfo{Name: fmt.Sprintf("%s_%d", queueName, 0)}, settings)
	if err != nil {
		return nil, err
	}

	for i := 0; i < 3; i++ {
		mq.queues[i] = ironmq.ConfigNew(fmt.Sprintf("%s_%d", queueName, i), settings)
	}

	logrus.WithFields(logrus.Fields{"base_queue": queueName}).Info("IronMQ initialized")
	return mq, nil
}

func (mq *IronMQ) Close() {}

func (mq *IronMQ) Push(ctx context.Context, job *models.Task) (*models.Task, error) {
	if err := validate(job); err != nil {
		return nil, err
	}

	// Push the work onto the queue.
	buf, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}
	_, err = mq.queues[*job.Priority].PushMessage(ironmq.Message{Body: string(buf), Delay: int64(job.Delay)})
	return job, err
}

func (mq *IronMQ) Reserve(ctx context.Context) (*models.Task, error) {
	var job models.Task

	var messages []ironmq.Message
	var err error
	for i := 2; i >= 0; i-- {
		messages, err = mq.queues[i].LongPoll(1, mq.reserveTimeout, 0 /* wait */, false /* delete */)
		if err != nil {
			// It is OK if the queue does not exist, it will be created when a message is queued.
			if !ironmq.ErrQueueNotFound(err) {
				return nil, err
			}
		}

		if len(messages) == 0 {
			// Try next priority.
			if i == 0 {
				return nil, nil
			}
			continue
		}

		// Found a message!
		break
	}

	message := messages[0]
	if message.Body == "" {
		return nil, nil
	}

	err = json.Unmarshal([]byte(message.Body), &job)
	if err != nil {
		return nil, err
	}
	mq.Lock()
	mq.msgAssoc[job.ID] = &assoc{message.Id, message.ReservationId}
	mq.Unlock()
	return &job, nil
}

func (mq *IronMQ) Delete(ctx context.Context, job *models.Task) error {
	if err := validate(job); err != nil {
		return err
	}
	mq.Lock()
	assoc, exists := mq.msgAssoc[job.ID]
	delete(mq.msgAssoc, job.ID)
	mq.Unlock()

	if exists {
		err := mq.queues[*job.Priority].DeleteMessage(assoc.msgId, assoc.reservationId)
		if err != nil && ErrReservationTimedOut(err) {
			return models.ErrMQTaskNotReserved
		}
		return err
	}
	return models.ErrMQTaskNotReserved
}

//TODO move to ironmq? (iron_go3/mq)
func ErrReservationTimedOut(err error) bool {
	return err.Error() == "403 Forbidden: Reservation has timed out"
}