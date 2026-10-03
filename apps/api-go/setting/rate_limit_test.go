package setting

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func preserveModelRequestRateLimitGroup(t *testing.T) {
	t.Helper()
	ModelRequestRateLimitMutex.Lock()
	previous := ModelRequestRateLimitGroup
	ModelRequestRateLimitMutex.Unlock()
	t.Cleanup(func() {
		ModelRequestRateLimitMutex.Lock()
		ModelRequestRateLimitGroup = previous
		ModelRequestRateLimitMutex.Unlock()
	})
}

func TestUpdateModelRequestRateLimitGroupRejectsInvalidJSONWithoutChangingLimits(t *testing.T) {
	preserveModelRequestRateLimitGroup(t)
	for _, invalid := range []string{`{"new":[2,4]`, `{"new":[2,4],"invalid":"not an array"}`} {
		t.Run(invalid, func(t *testing.T) {
			if err := UpdateModelRequestRateLimitGroupByJSONString(`{"original":[4,8]}`); err != nil {
				t.Fatal(err)
			}
			previous := ModelRequestRateLimitGroup2JSONString()
			if err := UpdateModelRequestRateLimitGroupByJSONString(invalid); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
			if got := ModelRequestRateLimitGroup2JSONString(); got != previous {
				t.Fatalf("failed update changed limits: got %s, want %s", got, previous)
			}
		})
	}
}

func TestUpdateModelRequestRateLimitGroupConcurrentReadersAndWriters(t *testing.T) {
	preserveModelRequestRateLimitGroup(t)
	configs := []string{`{"group":[4,8],"other":[6,12]}`, `{"group":[5,10],"other":[7,14]}`}
	if err := UpdateModelRequestRateLimitGroupByJSONString(configs[0]); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errors := make(chan error, 5)
	var wait sync.WaitGroup
	for i := range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for range 200 {
				if err := UpdateModelRequestRateLimitGroupByJSONString(configs[i]); err != nil {
					errors <- err
					return
				}
			}
		}()
	}
	for range 3 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for range 400 {
				total, success, found := GetGroupRateLimit("group")
				if !found || (total != 4 && total != 5) || success != 2*total {
					errors <- fmt.Errorf("incomplete group limits: total=%d success=%d found=%t", total, success, found)
					return
				}
				var snapshot map[string][2]int
				if err := json.Unmarshal([]byte(ModelRequestRateLimitGroup2JSONString()), &snapshot); err != nil {
					errors <- err
					return
				}
				group, other := snapshot["group"], snapshot["other"]
				if len(snapshot) != 2 || group[1] != 2*group[0] || other != [2]int{group[0] + 2, group[1] + 4} {
					errors <- fmt.Errorf("incomplete configuration snapshot: %v", snapshot)
					return
				}
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
