package jobqueue

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// FailureCallbackPayload describes the payload sent to a failure callback URL.
type FailureCallbackPayload struct {
	QueueName string `json:"queue_name"`
	Category  string `json:"category"`
	URL       string `json:"url"`
	Payload   string `json:"payload"`
	Status    string `json:"status"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
	FailCount uint   `json:"fail_count"`
}

func fireFailureCallback(failureURL string, job Job, res *Result, queueName string) {
	var category string
	if c, ok := job.(interface{ Category() string }); ok {
		category = c.Category()
	}

	payload := FailureCallbackPayload{
		QueueName: queueName,
		Category:  category,
		URL:       job.URL(),
		Payload:   job.Payload(),
		Status:    res.Status,
		Code:      res.Code,
		Message:   res.Message,
		FailCount: job.FailCount(),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Warn().Msgf("Failed to marshal failure callback payload: %s", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(failureURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		log.Warn().Msgf("Failed to send failure callback to %s: %s", failureURL, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Warn().Msgf("Failure callback to %s returned status %d", failureURL, resp.StatusCode)
	} else {
		log.Debug().Msgf("Failure callback sent to %s", failureURL)
	}
}
