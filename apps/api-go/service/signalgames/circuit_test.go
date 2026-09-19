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
