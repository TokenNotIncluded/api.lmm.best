package model

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// Reference: the eager copier in main@62e780a26b26a47c3a8fb450ba0a78ebce96ab0f.
// Keep the independent reference so changes to either path cannot hide a regression.
func routingSnapshotReference(source map[string]map[string][]int, removed map[int]struct{}) map[string]map[string][]int {
	cloned := make(map[string]map[string][]int, len(source))
	for group, models := range source {
		clonedModels := make(map[string][]int, len(models))
		for model, channels := range models {
			filtered := make([]int, 0, len(channels))
			for _, channelID := range channels {
				if _, remove := removed[channelID]; !remove {
					filtered = append(filtered, channelID)
				}
			}
			clonedModels[model] = filtered
		}
		cloned[group] = clonedModels
	}
	return cloned
}

func assertRoutingSnapshot(t *testing.T, source map[string]map[string][]int, removed map[int]struct{}) {
	t.Helper()
	before, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	want := routingSnapshotReference(source, removed)
	if got := cloneRoutingMapWithout(source, removed); !reflect.DeepEqual(got, want) {
		t.Fatalf("source=%v removed=%v: got %#v, want %#v", source, removed, got, want)
	}
	after, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("filter changed the source snapshot")
	}
}

func TestRoutingSnapshotEmptyAndInvalidIDs(t *testing.T) {
	t.Setenv("CHANNEL_ROUTING_SNAPSHOT_REUSE", "true")
	for _, source := range []map[string]map[string][]int{
		nil, {}, {"": nil}, {"": {"": nil, "empty": {}}},
		{"g": {"a": {-1, 0, 1, 1, -1}, "b": {1, 2}}},
	} {
		for _, removed := range []map[int]struct{}{nil, {}, {-1: {}}, {0: {}, 1: {}}, {-1: {}, 0: {}, 1: {}, 2: {}}} {
			assertRoutingSnapshot(t, source, removed)
		}
	}
}

func TestRoutingSnapshotExhaustiveSmallLists(t *testing.T) {
	t.Setenv("CHANNEL_ROUTING_SNAPSHOT_REUSE", "true")
	// All lists of length 0..5 over three IDs, and all eight removal sets.
	count := 1
	for length := 0; length <= 5; length++ {
		for word := 0; word < count; word++ {
			ids := make([]int, length)
			x := word
			for i := range ids {
				ids[i], x = x%3-1, x/3
			}
			for mask := 0; mask < 8; mask++ {
				removed := map[int]struct{}{}
				for i := 0; i < 3; i++ {
					if mask&(1<<i) != 0 {
						removed[i-1] = struct{}{}
					}
				}
				assertRoutingSnapshot(t, map[string]map[string][]int{"g": {"m": ids}}, removed)
			}
		}
		count *= 3
	}
}

func TestRoutingSnapshotSeededDifferential(t *testing.T) {
	t.Setenv("CHANNEL_ROUTING_SNAPSHOT_REUSE", "true")
	for _, seed := range []int64{110, 120, 125} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			for trial := 0; trial < 1000; trial++ {
				source := map[string]map[string][]int{}
				for g, groups := 0, rng.Intn(6); g < groups; g++ {
					models := map[string][]int{}
					for m, count := 0, rng.Intn(9); m < count; m++ {
						ids := make([]int, rng.Intn(41))
						for i := range ids {
							ids[i] = rng.Intn(33) - 16
						}
						models[fmt.Sprint(m)] = ids
					}
					source[fmt.Sprint(g)] = models
				}
				removed := map[int]struct{}{}
				for i, count := 0, rng.Intn(34); i < count; i++ {
					removed[rng.Intn(35)-17] = struct{}{}
				}
				assertRoutingSnapshot(t, source, removed)
				assertRoutingSnapshot(t, source, map[int]struct{}{rng.Intn(35) - 17: {}})
			}
		})
	}
}

func TestRoutingSnapshotStorageAndSuccessiveUpdates(t *testing.T) {
	t.Setenv("CHANNEL_ROUTING_SNAPSHOT_REUSE", "true")
	backing := []int{4, 5, 6, 77}
	source := map[string]map[string][]int{"g": {"same": backing[:3], "changed": {1, 2, 3}}}
	first := cloneRoutingMapWithout(source, map[int]struct{}{2: {}})
	second := cloneRoutingMapWithout(first, map[int]struct{}{1: {}})
	if !reflect.DeepEqual(source["g"]["changed"], []int{1, 2, 3}) ||
		!reflect.DeepEqual(first["g"]["changed"], []int{1, 3}) ||
		!reflect.DeepEqual(second["g"]["changed"], []int{3}) {
		t.Fatal("successive updates changed old snapshots or reordered IDs")
	}
	if &first["g"]["same"][0] != &source["g"]["same"][0] {
		t.Fatal("unchanged list was copied")
	}
	first["g"]["same"] = append(first["g"]["same"], 99)
	if backing[3] != 77 {
		t.Fatal("append overwrote the source array tail")
	}
	first["g"]["changed"][0] = 99
	first["g"]["new"] = []int{99}
	first["other"] = map[string][]int{}
	if source["g"]["changed"][0] != 1 || second["g"]["changed"][0] != 3 ||
		len(source) != 1 || len(source["g"]) != 2 {
		t.Fatal("changed lists or maps share writable storage")
	}
}

func TestRoutingSnapshotSwitch(t *testing.T) {
	for _, value := range []string{"", "false", "TRUE", "1", "true"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CHANNEL_ROUTING_SNAPSHOT_REUSE", value)
			for _, removed := range []map[int]struct{}{nil, {-1: {}}, {-1: {}, -2: {}}} {
				source := map[string]map[string][]int{"g": {"m": {1, 2}}}
				got := cloneRoutingMapWithout(source, removed)
				shared := &got["g"]["m"][0] == &source["g"]["m"][0]
				if shared != (value == "true" && len(removed) == 1) ||
					!reflect.DeepEqual(got, routingSnapshotReference(source, removed)) {
					t.Fatalf("switch %q removed=%v: shared=%v result=%v", value, removed, shared, got)
				}
			}
		})
	}
}

func TestRoutingSnapshotConcurrentReadersAndCopies(t *testing.T) {
	t.Setenv("CHANNEL_ROUTING_SNAPSHOT_REUSE", "true")
	source := routingSnapshotFixture(4, 32, 16)
	before, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 32; i++ {
				removed := map[int]struct{}{id + i: {}}
				first := cloneRoutingMapWithout(source, removed)
				second := cloneRoutingMapWithout(first, map[int]struct{}{id + i + 1: {}})
				want := routingSnapshotReference(source, map[int]struct{}{id + i: {}, id + i + 1: {}})
				if !reflect.DeepEqual(second, want) {
					t.Error("concurrent copies changed filtering results")
				}
			}
		}(worker)
	}
	wg.Wait()
	after, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("concurrent copies changed the shared source")
	}
}

func FuzzRoutingSnapshot(f *testing.F) {
	f.Setenv("CHANNEL_ROUTING_SNAPSHOT_REUSE", "true")
	f.Add([]byte{0, 1, 2, 1, 255})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 4096 {
			return
		}
		source := map[string]map[string][]int{"g": {"m": {}}}
		removed := map[int]struct{}{}
		for i, b := range data {
			if i%4 == 0 {
				removed[int(int8(b))] = struct{}{}
			} else {
				source["g"]["m"] = append(source["g"]["m"], int(int8(b)))
			}
		}
		assertRoutingSnapshot(t, source, removed)
		if len(data) > 0 {
			assertRoutingSnapshot(t, source, map[int]struct{}{int(int8(data[0])): {}})
		}
	})
}

func routingSnapshotFixture(groups, models, width int) map[string]map[string][]int {
	source := make(map[string]map[string][]int, groups)
	for g := 0; g < groups; g++ {
		rows := make(map[string][]int, models)
		for m := 0; m < models; m++ {
			ids := make([]int, width)
			for i := range ids {
				ids[i] = (m%32)*width + i + 1
			}
			rows[fmt.Sprintf("model-%04d", m)] = ids
		}
		source[fmt.Sprintf("group-%03d", g)] = rows
	}
	return source
}

var routingSnapshotSink map[string]map[string][]int

func BenchmarkRoutingSnapshot(b *testing.B) {
	for _, cfg := range []struct {
		name                  string
		groups, models, width int
	}{{"small", 2, 8, 4}, {"large", 32, 256, 64}} {
		source := routingSnapshotFixture(cfg.groups, cfg.models, cfg.width)
		all := map[int]struct{}{}
		mixed := map[int]struct{}{}
		for i := 1; i <= 32*cfg.width; i++ {
			all[i] = struct{}{}
			if i%2 == 1 {
				mixed[i] = struct{}{}
			}
		}
		for _, tc := range []struct {
			name    string
			removed map[int]struct{}
		}{{"sparse", map[int]struct{}{1: {}}}, {"absent", map[int]struct{}{-1: {}}}, {"dense", all}, {"mixed", mixed}} {
			for _, alg := range []struct {
				name string
				fn   func(map[string]map[string][]int, map[int]struct{}) map[string]map[string][]int
			}{{"baseline", routingSnapshotReference}, {"fallback", cloneRoutingMapWithout}, {"reuse", cloneRoutingMapWithout}} {
				b.Run(cfg.name+"/"+tc.name+"/"+alg.name, func(b *testing.B) {
					value := "false"
					if alg.name == "reuse" {
						value = "true"
					}
					b.Setenv("CHANNEL_ROUTING_SNAPSHOT_REUSE", value)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						routingSnapshotSink = alg.fn(source, tc.removed)
					}
				})
			}
		}
	}
}
