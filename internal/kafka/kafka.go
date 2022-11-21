package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"path/filepath"
	"strings"
	"time"

	"github.com/RHEnVision/provisioning-backend/internal/config"
	"github.com/RHEnVision/provisioning-backend/internal/ctxval"
	"github.com/RHEnVision/provisioning-backend/internal/version"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
	"github.com/segmentio/kafka-go/sasl/scram"
)

type kafkaBroker struct {
	dialer    *kafka.Dialer
	transport *kafka.Transport
}

var _ Broker = &kafkaBroker{}

var (
	DifferentTopicErr       = errors.New("messages in batch have different topics")
	UnknownSaslMechanismErr = errors.New("unknown SASL mechanism")
)

func createSASLMechanism(saslMechanismName string, username string, password string) (sasl.Mechanism, error) {
	switch strings.ToLower(saslMechanismName) {
	case "plain":
		return plain.Mechanism{
			Username: username,
			Password: password,
		}, nil
	case "scram-sha-512":
		mechanism, err := scram.Mechanism(scram.SHA512, username, password)
		if err != nil {
			return nil, fmt.Errorf("unable to create scram-sha-512 mechanism: %w", err)
		}

		return mechanism, nil
	case "scram-sha-256":
		mechanism, err := scram.Mechanism(scram.SHA256, username, password)
		if err != nil {
			return nil, fmt.Errorf("unable to create scram-sha-256 mechanism: %w", err)
		}

		return mechanism, nil
	default:
		return nil, fmt.Errorf("%w: %s", UnknownSaslMechanismErr, saslMechanismName)
	}
}

func InitializeKafkaBroker() error {
	var err error
	broker, err = NewKafkaBroker()
	if err != nil {
		return fmt.Errorf("unable to initialize kafka: %w", err)
	}

	return nil
}

func NewKafkaBroker() (Broker, error) {
	var tlsConfig *tls.Config
	var saslMechanism sasl.Mechanism

	// configure TLS when CA certificate was provided
	if config.Kafka.CACert != "" {
		cleanCAPath := filepath.Clean(config.Kafka.CACert)
		pemCerts, err := ioutil.ReadFile(cleanCAPath)
		if err != nil {
			return nil, fmt.Errorf("unable to read kafka CA cert: %w", err)
		}

		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(pemCerts)

		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    pool,
		}
	}

	// configure SASL if mechanism was provided
	if config.Kafka.SASL.SaslMechanism != "" {
		var err error
		saslMechanism, err = createSASLMechanism(config.Kafka.SASL.SaslMechanism, config.Kafka.SASL.Username, config.Kafka.SASL.Password)
		if err != nil {
			return nil, fmt.Errorf("kafka SASL error: %w", err)
		}
	}

	dialer := &kafka.Dialer{
		ClientID:      version.KafkaClientID,
		Timeout:       10 * time.Second,
		SASLMechanism: saslMechanism,
		TLS:           tlsConfig,
	}

	transport := &kafka.Transport{
		Dial:     dialer.DialFunc,
		ClientID: version.KafkaClientID,
		TLS:      tlsConfig,
		SASL:     saslMechanism,
	}

	return &kafkaBroker{
		dialer:    dialer,
		transport: transport,
	}, nil
}

func newContextLogger(ctx context.Context) func(msg string, a ...interface{}) {
	return func(msg string, a ...interface{}) {
		logger := ctxval.Logger(ctx)
		logger.Debug().Bool("kafka", true).Msgf(msg, a...)
	}
}

func newContextErrLogger(ctx context.Context) func(msg string, a ...interface{}) {
	return func(msg string, a ...interface{}) {
		logger := ctxval.Logger(ctx)
		logger.Warn().Bool("kafka", true).Msgf(msg, a...)
	}
}

// NewReader creates a reader. Use Close() function to close the reader.
func (b *kafkaBroker) NewReader(ctx context.Context, topic string) *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:     config.Kafka.Brokers,
		Dialer:      b.dialer,
		Topic:       topic,
		StartOffset: kafka.LastOffset,
		Logger:      kafka.LoggerFunc(newContextLogger(ctx)),
		ErrorLogger: kafka.LoggerFunc(newContextErrLogger(ctx)),
	})
}

// NewWriter creates synchronous writer created from the pool. It does not have associated any topic with it,
// therefore topic must be set on the message-level. Make sure to close it with Close() function.
func (b *kafkaBroker) NewWriter(ctx context.Context) *kafka.Writer {
	return &kafka.Writer{
		Addr:        kafka.TCP(config.Kafka.Brokers...),
		Transport:   b.transport,
		Logger:      kafka.LoggerFunc(newContextLogger(ctx)),
		ErrorLogger: kafka.LoggerFunc(newContextErrLogger(ctx)),
	}
}

// Consume reads messages in batches up to 1 MB with up to 10 seconds delay. It blocks, therefore
// it should be called from a separate goroutine. Use context cancellation to stop the loop.
func (b *kafkaBroker) Consume(ctx context.Context, topic string, handler func(ctx context.Context, message *GenericMessage)) {
	logger := ctxval.Logger(ctx)
	r := b.NewReader(ctx, topic)
	defer r.Close()

	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil && errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			logger.Warn().Err(err).Msgf("Error when reading message: %s", err.Error())
		} else {
			logger.Trace().Bytes("payload", msg.Value).Msgf("Received message with key: %s", msg.Key)
			handler(ctx, NewMessageFromKafka(&msg))
		}
	}
}

// Send one or more generic messages with the same topic. If there is a message with
// different topic than the first one, DifferentTopicErr is returned.
func (b *kafkaBroker) Send(ctx context.Context, messages ...*GenericMessage) error {
	if len(messages) == 0 {
		return nil
	}

	commonTopic := messages[0].Topic
	w := b.NewWriter(ctx)
	defer w.Close()

	kMessages := make([]kafka.Message, len(messages))
	for i, m := range messages {
		if m.Topic != commonTopic {
			return DifferentTopicErr
		}
		kMessages[i] = m.KafkaMessage()
	}

	err := w.WriteMessages(ctx, kMessages...)
	if err != nil {
		return fmt.Errorf("cannot send kafka messages(s): %w", err)
	}

	return nil
}