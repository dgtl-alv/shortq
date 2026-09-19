package clickqueue

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"shortq/internal/db"
	"shortq/internal/models"
	"shortq/internal/store"
)

func TestAnalyticsWorkerPersistsDuplicateOnceUsingOccurredAt(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	rabbitURL := os.Getenv("TEST_RABBITMQ_URL")
	if dsn == "" || rabbitURL == "" {
		t.Skip("TEST_DATABASE_URL and TEST_RABBITMQ_URL are required")
	}
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	st := store.New(database)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenant, err := st.EnsureTenant("Worker Tenant "+suffix, "worker-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	email := "worker-" + suffix + "@example.test"
	if err := st.CreateUser(email, "Worker User", "customer", &tenant.ID, []byte("hash")); err != nil {
		t.Fatal(err)
	}
	user, _, err := st.UserByEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	link, err := st.CreateLink(user, models.Link{Slug: "worker-" + suffix, TargetURL: "https://example.com", RedirectCode: 302, ForwardQuery: true}, nil)
	if err != nil {
		t.Fatal(err)
	}

	adminConnection, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConnection.Close()
	adminChannel, err := adminConnection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer adminChannel.Close()
	_, _ = adminChannel.QueueDelete(QueueName, false, false, false)
	_, _ = adminChannel.QueueDelete(DeadLetterQueueName, false, false, false)
	_, _ = adminChannel.QueueDelete(RetryQueueName, false, false, false)
	_ = adminChannel.ExchangeDelete(ExchangeName, false, false)
	_ = adminChannel.ExchangeDelete(DeadLetterExchangeName, false, false)
	_ = adminChannel.ExchangeDelete(RetryExchangeName, false, false)

	metrics := &WorkerMetrics{}
	worker, err := NewRabbitMQWorker(rabbitURL, st, metrics)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	deadline := time.Now().Add(8 * time.Second)
	for {
		probe, probeErr := adminConnection.Channel()
		if probeErr != nil {
			cancel()
			t.Fatal(probeErr)
		}
		_, inspectErr := probe.QueueInspect(QueueName)
		_ = probe.Close()
		if inspectErr == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("worker did not declare topology")
		}
		time.Sleep(20 * time.Millisecond)
	}
	deadline = time.Now().Add(8 * time.Second)

	publisher, err := NewRabbitMQPublisher(rabbitURL, time.Second, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	event := models.ClickEvent{
		EventID: newIntegrationUUID(t), LinkID: link.ID, Slug: link.Slug,
		Method: "GET", StatusCode: 302, RouteType: "default", CountryCode: "ID",
		OccurredAt: time.Date(2026, 9, 18, 23, 30, 0, 0, time.FixedZone("UTC-7", -7*60*60)),
		Increment:  true,
	}
	if err := publisher.Publish(context.Background(), event); err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), event); err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := publisher.Close(); err != nil {
		cancel()
		t.Fatal(err)
	}

	var rawClicks, linkClicks, rollupClicks int64
	var rollupDay string
	for {
		err = database.QueryRow(`SELECT COUNT(*) FROM clicks WHERE event_id=$1`, event.EventID).Scan(&rawClicks)
		if err == nil && rawClicks == 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("worker did not persist event: count=%d err=%v", rawClicks, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := database.QueryRow(`SELECT clicks FROM links WHERE id=$1`, link.ID).Scan(&linkClicks); err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT clicks,day::text FROM click_rollups_daily WHERE link_id=$1`, link.ID).Scan(&rollupClicks, &rollupDay); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not shut down")
	}
	if rawClicks != 1 || linkClicks != 1 || rollupClicks != 1 || rollupDay != "2026-09-19" {
		t.Fatalf("raw=%d link=%d rollup=%d day=%s", rawClicks, linkClicks, rollupClicks, rollupDay)
	}
	snapshot := metrics.Snapshot()
	if snapshot["committed"] != 1 || snapshot["duplicate"] != 1 {
		t.Fatalf("worker metrics = %#v", snapshot)
	}
}

func newIntegrationUUID(t *testing.T) string {
	t.Helper()
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal(err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}
