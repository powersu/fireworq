package stats

import "github.com/fireworq/fireworq/jobqueue"

// JobQueue wraps a jobqueue.JobQueue to record dispatch statistics in
// Redis after each job completion. All other methods are delegated to
// the inner queue unchanged.
type JobQueue struct {
	jobqueue.JobQueue
	writer *Writer
}

// NewJobQueue returns a stats-tracking decorator around inner. If
// writer is nil the inner queue is returned as-is (no-op).
func NewJobQueue(inner jobqueue.JobQueue, writer *Writer) jobqueue.JobQueue {
	if writer == nil {
		return inner
	}
	return &JobQueue{JobQueue: inner, writer: writer}
}

// Complete delegates to the inner queue and then asynchronously writes
// dispatch statistics to Redis. The result classification mirrors the
// logic in jobqueue.(*jobQueue).Complete.
func (q *JobQueue) Complete(job jobqueue.Job, res *jobqueue.Result) {
	// Capture subscribe_id before delegation (job may be deleted after)
	subscribeID := ExtractSubscribeID(job.FailureURL())

	// Delegate to the real queue first
	q.JobQueue.Complete(job, res)

	if subscribeID == "" {
		return
	}

	isSuccess := res.IsSuccess()
	// Permanent fail: explicit permanent-failure status, or retries exhausted
	isPermanentFail := res.IsPermanentFailure() || (!isSuccess && job.RetryCount() == 0)

	go q.writer.RecordDispatch(subscribeID, isSuccess, isPermanentFail)
}
