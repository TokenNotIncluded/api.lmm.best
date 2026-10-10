// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package signalgames

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func solve(c Circuit, size int) []int {
	tiles := append([]int(nil), c.Tiles...)
	var actions []int
	for _, index := range c.Route {
		for tiles[index] != c.Solution[index] {
			if Won(tiles, size) {
				return actions
			}
			tiles[index] = Rotate(tiles[index])
			actions = append(actions, index)
		}
	}
	return actions
}
func TestSignalGameCrossLanguageGolden(t *testing.T) {
	raw, err := os.ReadFile("../../../../contracts/signal-game/v2-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Size     int    `json:"size"`
		Seed     uint32 `json:"seed"`
		Tiles    string `json:"tiles_sha256"`
		Solution string `json:"solution_sha256"`
		Length   int    `json:"route_length"`
	}
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	digest := func(values []int) string {
		bytes := make([]byte, len(values))
		for i, v := range values {
			bytes[i] = byte(v)
		}
		hash := sha256.Sum256(bytes)
		return hex.EncodeToString(hash[:])
	}
	for _, row := range rows {
		c := Generate(row.Seed, row.Size)
		if digest(c.Tiles) != row.Tiles || digest(c.Solution) != row.Solution || len(c.Route) != row.Length {
			t.Fatalf("JavaScript and Go disagree: size=%d seed=%d", row.Size, row.Seed)
		}
		if Won(c.Tiles, row.Size) || !Won(c.Solution, row.Size) {
			t.Fatal("invalid generated board")
		}
		actions := solve(c, row.Size)
		if _, err := Replay(row.Seed, row.Size, actions, false); err != nil {
			t.Fatal(err)
		}
	}
}
func TestSignalGameReplayRejectsForgedChallenge(t *testing.T) {
	c := Generate(42, 5)
	actions := solve(c, 5)
	for _, input := range [][]int{nil, {-1}, {25}, append(append([]int(nil), actions...), 0), make([]int, MaxMoves(5)+1)} {
		if _, err := Replay(42, 5, input, false); err == nil {
			t.Fatalf("accepted invalid challenge %v", input)
		}
	}
	hints := make([]int, 0)
	tiles := append([]int(nil), c.Tiles...)
	for !Won(tiles, 5) {
		for _, index := range c.Route {
			if tiles[index] != c.Solution[index] {
				tiles[index] = Rotate(tiles[index])
				hints = append(hints, -1)
				break
			}
		}
	}
	if count, err := Replay(42, 5, hints, true); err != nil || count != len(hints) {
		t.Fatal("practice hints rejected", err)
	}
	if _, err := Replay(42, 5, hints, false); err == nil {
		t.Fatal("challenge accepted hints")
	}
}

func TestSignalGameReplayBudgetScalesWithBoard(t *testing.T) {
	for _, size := range []int{5, 8, 12, 24, 48, 64} {
		if got, want := MaxMoves(size), size*size*4; got != want {
			t.Fatalf("MaxMoves(%d) = %d, want %d", size, got, want)
		}
	}
}

func TestSignalGameReplayDoesNotAllocatePerAction(t *testing.T) {
	const size = 64
	c := Generate(42, size)
	solution := solve(c, size)
	onRoute := make(map[int]bool, len(c.Route))
	for _, index := range c.Route {
		onRoute[index] = true
	}
	paddingTile := 0
	for onRoute[paddingTile] {
		paddingTile++
	}
	padding := make([]int, MaxMoves(size)-len(solution))
	padding = padding[:len(padding)/4*4]
	for i := range padding {
		padding[i] = paddingTile
	}
	padded := append(padding, solution...)
	if _, err := Replay(42, size, padded, false); err != nil {
		t.Fatal(err)
	}
	baseAllocs := testing.AllocsPerRun(1, func() { _, _ = Replay(42, size, solution, false) })
	paddedAllocs := testing.AllocsPerRun(1, func() { _, _ = Replay(42, size, padded, false) })
	if paddedAllocs > baseAllocs+2 {
		t.Fatalf("replay allocations grew with actions: base %.0f, padded %.0f", baseAllocs, paddedAllocs)
	}
}
