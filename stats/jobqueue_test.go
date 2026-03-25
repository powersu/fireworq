package stats

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fireworq/fireworq/jobqueue"
	"github.com/fireworq/fireworq/jobqueue/logger"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// --- mock job ---

type mockJob struct {
	url        string
	payload    string
	timeout    uint
	retryCount uint
	retryDelay uint
	failCount  uint
	failureURL string
}

func (j *mockJob) URL() string                    { return j.url }
func (j *mockJob) Payload() string                { return j.payload }
func (j *mockJob) Timeout() uint                  { return j.timeout }
func (j *mockJob) RetryCount() uint               { return j.retryCount }
func (j *mockJob) RetryDelay() uint               { return j.retryDelay }
func (j *mockJob) FailCount() uint                { return j.failCount }
func (j *mockJob) FailureURL() string             { return j.failureURL }
func (j *mockJob) ToLoggable() logger.LoggableJob { return nil }

// --- mock jobqueue ---

type mockJobQueue struct {
	mu        sync.Mutex
	completed []completedRecord
}

type completedRecord struct {
	job jobqueue.Job
	res *jobqueue.Result
}

func (q *mockJobQueue) Stop() <-chan struct{}                         { return nil }
func (q *mockJobQueue) Push(job jobqueue.IncomingJob) (uint64, error) { return 0, nil }
func (q *mockJobQueue) Pop(limit uint) ([]jobqueue.Job, error)        { return nil, nil }
func (q *mockJobQueue) Name() string                                  { return "test" }
func (q *mockJobQueue) IsActive() bool                                { return true }
func (q *mockJobQueue) Node() (*jobqueue.Node, error)                 { return nil, nil }
func (q *mockJobQueue) Stats() *jobqueue.Stats                        { return nil }
func (q *mockJobQueue) Inspector() (jobqueue.Inspector, bool)         { return nil, false }
func (q *mockJobQueue) FailureLog() (jobqueue.FailureLog, bool)       { return nil, false }

func (q *mockJobQueue) Complete(job jobqueue.Job, res *jobqueue.Result) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.completed = append(q.completed, completedRecord{job, res})
}

// --- tests ---

func TestJobQueueDecorator_Success(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	writer := NewWriter(client)
	inner := &mockJobQueue{}

	jq := NewJobQueue(inner, writer)

	job := &mockJob{
		failureURL: "http://example.com/cb?org_id=191&target_env=test",
		retryCount: 3,
	}
	res := &jobqueue.Result{Status: jobqueue.ResultStatusSuccess}

	jq.Complete(job, res)

	// Wait for async goroutine
	time.Sleep(100 * time.Millisecond)

	// Verify inner queue was called
	if len(inner.completed) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(inner.completed))
	}

	// Verify Redis keys
	now := time.Now()
	fiveMinKey, _, _ := bucketKeys("test", "191", now)
	assertHashField(t, mr, fiveMinKey, "total", "1")
	assertHashField(t, mr, fiveMinKey, "success", "1")
	assertActiveMember(t, mr, "test", "191")
}

func TestJobQueueDecorator_RetryableFail(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	writer := NewWriter(client)
	inner := &mockJobQueue{}

	jq := NewJobQueue(inner, writer)

	job := &mockJob{
		failureURL: "http://example.com/cb?org_id=200&target_env=test",
		retryCount: 2, // has retries left
	}
	res := &jobqueue.Result{Status: jobqueue.ResultStatusFailure}

	jq.Complete(job, res)
	time.Sleep(100 * time.Millisecond)

	now := time.Now()
	fiveMinKey, _, _ := bucketKeys("test", "200", now)
	assertHashField(t, mr, fiveMinKey, "total", "1")
	assertHashField(t, mr, fiveMinKey, "fail", "1")
	assertHashFieldMissing(t, mr, fiveMinKey, "permanent_fail")
}

func TestJobQueueDecorator_PermanentFail_ExplicitStatus(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	writer := NewWriter(client)
	inner := &mockJobQueue{}

	jq := NewJobQueue(inner, writer)

	job := &mockJob{
		failureURL: "http://example.com/cb?org_id=300&target_env=test",
		retryCount: 5, // retries left, but status is permanent
	}
	res := &jobqueue.Result{Status: jobqueue.ResultStatusPermanentFailure}

	jq.Complete(job, res)
	time.Sleep(100 * time.Millisecond)

	now := time.Now()
	fiveMinKey, _, _ := bucketKeys("test", "300", now)
	assertHashField(t, mr, fiveMinKey, "total", "1")
	assertHashField(t, mr, fiveMinKey, "fail", "1")
	assertHashField(t, mr, fiveMinKey, "permanent_fail", "1")
}

func TestJobQueueDecorator_PermanentFail_RetriesExhausted(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	writer := NewWriter(client)
	inner := &mockJobQueue{}

	jq := NewJobQueue(inner, writer)

	job := &mockJob{
		failureURL: "http://example.com/cb?org_id=400&target_env=test",
		retryCount: 0, // no retries left
	}
	res := &jobqueue.Result{Status: jobqueue.ResultStatusFailure}

	jq.Complete(job, res)
	time.Sleep(100 * time.Millisecond)

	now := time.Now()
	fiveMinKey, _, _ := bucketKeys("test", "400", now)
	assertHashField(t, mr, fiveMinKey, "total", "1")
	assertHashField(t, mr, fiveMinKey, "fail", "1")
	assertHashField(t, mr, fiveMinKey, "permanent_fail", "1")
}

func TestJobQueueDecorator_NoFailureURL_SkipsStats(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	writer := NewWriter(client)
	inner := &mockJobQueue{}

	jq := NewJobQueue(inner, writer)

	job := &mockJob{failureURL: ""} // no failure URL
	res := &jobqueue.Result{Status: jobqueue.ResultStatusSuccess}

	jq.Complete(job, res)
	time.Sleep(100 * time.Millisecond)

	// Inner queue should still be called
	if len(inner.completed) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(inner.completed))
	}

	// No Redis keys should exist
	keys := mr.Keys()
	if len(keys) != 0 {
		t.Errorf("expected no Redis keys, got %v", keys)
	}
}

func TestJobQueueDecorator_NoOrgID_SkipsStats(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	writer := NewWriter(client)
	inner := &mockJobQueue{}

	jq := NewJobQueue(inner, writer)

	// failure URL exists but no org_id param
	job := &mockJob{failureURL: "http://example.com/cb?target_env=test"}
	res := &jobqueue.Result{Status: jobqueue.ResultStatusSuccess}

	jq.Complete(job, res)
	time.Sleep(100 * time.Millisecond)

	if len(inner.completed) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(inner.completed))
	}

	keys := mr.Keys()
	if len(keys) != 0 {
		t.Errorf("expected no Redis keys, got %v", keys)
	}
}

func TestJobQueueDecorator_NoTargetEnv_SkipsStats(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	writer := NewWriter(client)
	inner := &mockJobQueue{}

	jq := NewJobQueue(inner, writer)

	// failure URL has org_id but no target_env
	job := &mockJob{failureURL: "http://example.com/cb?org_id=191"}
	res := &jobqueue.Result{Status: jobqueue.ResultStatusSuccess}

	jq.Complete(job, res)
	time.Sleep(100 * time.Millisecond)

	if len(inner.completed) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(inner.completed))
	}

	keys := mr.Keys()
	if len(keys) != 0 {
		t.Errorf("expected no Redis keys, got %v", keys)
	}
}

func TestJobQueueDecorator_NilWriter_ReturnsInner(t *testing.T) {
	inner := &mockJobQueue{}
	jq := NewJobQueue(inner, nil)

	// Should return inner directly, not a wrapper
	if _, ok := jq.(*JobQueue); ok {
		t.Error("expected inner queue returned directly when writer is nil")
	}
}

func TestJobQueueDecorator_MultipleDispatches(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	writer := NewWriter(client)
	inner := &mockJobQueue{}

	jq := NewJobQueue(inner, writer)

	// Simulate 3 dispatches for same org
	for i := 0; i < 3; i++ {
		job := &mockJob{
			failureURL: "http://example.com/cb?org_id=500&target_env=test",
			retryCount: 2,
		}
		res := &jobqueue.Result{Status: jobqueue.ResultStatusSuccess}
		jq.Complete(job, res)
	}

	time.Sleep(200 * time.Millisecond)

	now := time.Now()
	fiveMinKey, _, _ := bucketKeys("test", "500", now)
	assertHashField(t, mr, fiveMinKey, "total", "3")
	assertHashField(t, mr, fiveMinKey, "success", "3")

	// Active set should have exactly 1 member (dedup by ZADD)
	activeKey := fmt.Sprintf("webhook:%s:active_subscribes", "test")
	members, err := mr.ZMembers(activeKey)
	if err != nil {
		t.Fatalf("ZMembers error: %v", err)
	}
	if len(members) != 1 || members[0] != "500" {
		t.Errorf("expected [500], got %v", members)
	}
}

func TestBucketKeyFormat(t *testing.T) {
	// Verify 5-min boundary truncation
	tests := []struct {
		minute     int
		wantBucket int
	}{
		{0, 0}, {1, 0}, {4, 0},
		{5, 5}, {9, 5},
		{10, 10}, {14, 10},
		{55, 55}, {59, 55},
	}

	for _, tt := range tests {
		now := time.Date(2026, 3, 16, 14, tt.minute, 0, 0, time.UTC)
		fiveMinKey, oneHourKey, dailyKey := bucketKeys("staging", "100", now)

		wantFiveMin := fmt.Sprintf("webhook:staging:stats:100:5m:2026031614%02d", tt.wantBucket)
		if fiveMinKey != wantFiveMin {
			t.Errorf("minute=%d: fiveMinKey = %q, want %q", tt.minute, fiveMinKey, wantFiveMin)
		}

		wantOneHour := "webhook:staging:stats:100:1h:2026031614"
		if oneHourKey != wantOneHour {
			t.Errorf("minute=%d: oneHourKey = %q, want %q", tt.minute, oneHourKey, wantOneHour)
		}

		wantDaily := "webhook:staging:stats:100:1d:20260316"
		if dailyKey != wantDaily {
			t.Errorf("minute=%d: dailyKey = %q, want %q", tt.minute, dailyKey, wantDaily)
		}
	}
}
