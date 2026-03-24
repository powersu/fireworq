package stats

import (
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const testEnv = "test"

func setupMiniredis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return mr, client
}

func bucketKeys(env string, orgID string, now time.Time) (fiveMin string, oneHour string) {
	bucket := now.Minute() - (now.Minute() % 5)
	fiveMin = fmt.Sprintf("webhook:%s:stats:%s:5m:%s%02d", env, orgID, now.Format("2006010215"), bucket)
	oneHour = fmt.Sprintf("webhook:%s:stats:%s:1h:%s", env, orgID, now.Format("2006010215"))
	return
}

func TestRecordDispatch_Success(t *testing.T) {
	mr, client := setupMiniredis(t)
	w := NewWriter(client)

	w.RecordDispatch(testEnv, "100", true, false)

	now := time.Now()
	fiveMinKey, oneHourKey := bucketKeys(testEnv, "100", now)

	for _, key := range []string{fiveMinKey, oneHourKey} {
		assertHashField(t, mr, key, "total", "1")
		assertHashField(t, mr, key, "success", "1")
		assertHashFieldMissing(t, mr, key, "fail")
		assertHashFieldMissing(t, mr, key, "permanent_fail")
	}

	assertActiveMember(t, mr, testEnv, "100")
}

func TestRecordDispatch_RetryableFail(t *testing.T) {
	mr, client := setupMiniredis(t)
	w := NewWriter(client)

	w.RecordDispatch(testEnv, "200", false, false)

	now := time.Now()
	fiveMinKey, oneHourKey := bucketKeys(testEnv, "200", now)

	for _, key := range []string{fiveMinKey, oneHourKey} {
		assertHashField(t, mr, key, "total", "1")
		assertHashField(t, mr, key, "fail", "1")
		assertHashFieldMissing(t, mr, key, "success")
		assertHashFieldMissing(t, mr, key, "permanent_fail")
	}
}

func TestRecordDispatch_PermanentFail(t *testing.T) {
	mr, client := setupMiniredis(t)
	w := NewWriter(client)

	w.RecordDispatch(testEnv, "300", false, true)

	now := time.Now()
	fiveMinKey, oneHourKey := bucketKeys(testEnv, "300", now)

	for _, key := range []string{fiveMinKey, oneHourKey} {
		assertHashField(t, mr, key, "total", "1")
		assertHashField(t, mr, key, "fail", "1")
		assertHashField(t, mr, key, "permanent_fail", "1")
		assertHashFieldMissing(t, mr, key, "success")
	}
}

func TestRecordDispatch_MultipleCalls_Accumulate(t *testing.T) {
	mr, client := setupMiniredis(t)
	w := NewWriter(client)

	w.RecordDispatch(testEnv, "400", true, false)
	w.RecordDispatch(testEnv, "400", true, false)
	w.RecordDispatch(testEnv, "400", false, false)
	w.RecordDispatch(testEnv, "400", false, true)

	now := time.Now()
	fiveMinKey, _ := bucketKeys(testEnv, "400", now)

	assertHashField(t, mr, fiveMinKey, "total", "4")
	assertHashField(t, mr, fiveMinKey, "success", "2")
	assertHashField(t, mr, fiveMinKey, "fail", "2")
	assertHashField(t, mr, fiveMinKey, "permanent_fail", "1")
}

func TestRecordDispatch_TTL(t *testing.T) {
	mr, client := setupMiniredis(t)
	w := NewWriter(client)

	w.RecordDispatch(testEnv, "500", true, false)

	now := time.Now()
	fiveMinKey, oneHourKey := bucketKeys(testEnv, "500", now)

	ttl5m := mr.TTL(fiveMinKey)
	if ttl5m != 2*time.Hour {
		t.Errorf("5-min bucket TTL = %v, want %v", ttl5m, 2*time.Hour)
	}

	ttl1h := mr.TTL(oneHourKey)
	if ttl1h != 25*time.Hour {
		t.Errorf("1-hour bucket TTL = %v, want %v", ttl1h, 25*time.Hour)
	}
}

func TestRecordDispatch_EmptyOrgID(t *testing.T) {
	mr, client := setupMiniredis(t)
	w := NewWriter(client)

	w.RecordDispatch(testEnv, "", true, false)

	keys := mr.Keys()
	if len(keys) != 0 {
		t.Errorf("expected no keys for empty org_id, got %v", keys)
	}
}

func TestRecordDispatch_EmptyTargetEnv(t *testing.T) {
	mr, client := setupMiniredis(t)
	w := NewWriter(client)

	w.RecordDispatch("", "100", true, false)

	keys := mr.Keys()
	if len(keys) != 0 {
		t.Errorf("expected no keys for empty target_env, got %v", keys)
	}
}

func TestRecordDispatch_NilWriter(t *testing.T) {
	var w *Writer
	// should not panic
	w.RecordDispatch(testEnv, "100", true, false)
}

func TestRecordDispatch_ActiveSubscribers(t *testing.T) {
	mr, client := setupMiniredis(t)
	w := NewWriter(client)

	w.RecordDispatch(testEnv, "600", true, false)
	w.RecordDispatch(testEnv, "700", false, false)

	activeKey := fmt.Sprintf("webhook:%s:active_subscribes", testEnv)
	members, err := mr.ZMembers(activeKey)
	if err != nil {
		t.Fatalf("failed to get sorted set members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}

	found := map[string]bool{}
	for _, m := range members {
		found[m] = true
	}
	if !found["600"] || !found["700"] {
		t.Errorf("expected members 600 and 700, got %v", members)
	}
}

// --- helpers ---

func assertHashField(t *testing.T, mr *miniredis.Miniredis, key, field, want string) {
	t.Helper()
	got := mr.HGet(key, field)
	if got != want {
		t.Errorf("HGet(%s, %s) = %q, want %q", key, field, got, want)
	}
}

func assertHashFieldMissing(t *testing.T, mr *miniredis.Miniredis, key, field string) {
	t.Helper()
	got := mr.HGet(key, field)
	if got != "" {
		t.Errorf("expected field %s in key %s to be missing, but got %q", field, key, got)
	}
}

func assertActiveMember(t *testing.T, mr *miniredis.Miniredis, env string, member string) {
	t.Helper()
	activeKey := fmt.Sprintf("webhook:%s:active_subscribes", env)
	members, err := mr.ZMembers(activeKey)
	if err != nil {
		t.Fatalf("failed to get sorted set members: %v", err)
	}
	for _, m := range members {
		if m == member {
			return
		}
	}
	t.Errorf("expected member %q in %s, got %v", member, activeKey, members)
}
