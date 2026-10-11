// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package signalgames

import (
	"fmt"
	"testing"
)

// Keep the board one turn from completion while spending the remaining public
// move budget on an unrelated tile. Every four turns restores that tile.
func paddedSignalSolution(t testing.TB, size int) []int {
	t.Helper()
	c := Generate(42, size)
	solution := solve(c, size)
	if len(solution) == 0 {
		t.Fatal("fixture was already solved")
	}
	tiles := append([]int(nil), c.Tiles...)
	for _, index := range solution[:len(solution)-1] {
		tiles[index] = Rotate(tiles[index])
	}
	onRoute := make(map[int]bool, len(c.Route))
	for _, index := range c.Route {
		onRoute[index] = true
	}
	paddingTile := -1
	for index := range tiles {
		if onRoute[index] {
			continue
		}
		safe := true
		for turn := 0; turn < 4; turn++ {
			tiles[index] = Rotate(tiles[index])
			if Won(tiles, size) {
				safe = false
			}
		}
		if safe {
			paddingTile = index
			break
		}
	}
	if paddingTile < 0 {
		t.Fatal("fixture has no safe padding tile")
	}
	padding := (MaxMoves(size) - len(solution)) / 4 * 4
	actions := make([]int, 0, len(solution)+padding)
	actions = append(actions, solution[:len(solution)-1]...)
	for i := 0; i < padding; i++ {
		actions = append(actions, paddingTile)
	}
	return append(actions, solution[len(solution)-1])
}

func TestSignalGameReplayAllocationDoesNotScaleWithMoves(t *testing.T) {
	actions := paddedSignalSolution(t, 24)
	short := actions[:1]
	long := actions[:257]
	shortAllocs := testing.AllocsPerRun(2, func() { _, _ = Replay(42, 24, short, false) })
	longAllocs := testing.AllocsPerRun(2, func() { _, _ = Replay(42, 24, long, false) })
	if longAllocs > shortAllocs+1 {
		t.Fatalf("per-move allocations remain: short=%g long=%g", shortAllocs, longAllocs)
	}
}

func TestSignalGameReplayPreservesPublicMoveBudget(t *testing.T) {
	for _, size := range []int{5, 8, 12, 24, 48, 64} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			actions := paddedSignalSolution(t, size)
			if MaxMoves(size)-len(actions) > 3 {
				t.Fatal("fixture did not exercise the public move budget")
			}
			if _, err := Replay(42, size, actions, false); err != nil {
				t.Fatalf("legitimate long challenge rejected: %v", err)
			}
			if _, err := Replay(42, size, append(actions, 0), false); err == nil {
				t.Fatal("accepted a move after completion")
			}
			if _, err := Replay(42, size, make([]int, MaxMoves(size)+1), true); err == nil {
				t.Fatal("practice accepted an oversized replay")
			}
		})
	}
}

func BenchmarkSignalGameReplayPublicBudget(b *testing.B) {
	actions := paddedSignalSolution(b, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Replay(42, 64, actions, false); err != nil {
			b.Fatal(err)
		}
	}
}
