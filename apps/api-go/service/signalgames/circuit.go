// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package signalgames

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"
)

const RulesVersion = 2

var ports = []int{1, 2, 4, 8}
var shapes = []int{3, 6, 12, 9, 5, 10}

func ValidSize(size int) bool {
	switch size {
	case 5, 8, 12, 24, 48, 64:
		return true
	}
	return false
}
func MaxMoves(size int) int {
	if size*size*16 < 400 {
		return 400
	}
	return size * size * 16
}

type Circuit struct {
	Tiles    []int `json:"tiles"`
	Solution []int `json:"-"`
	Route    []int `json:"-"`
}

func Rotate(mask int) int { return ((mask << 1) & 15) | (mask >> 3) }
func neighbor(index, port, size int) int {
	row, col := index/size, index%size
	switch port {
	case 1:
		if row > 0 {
			return index - size
		}
	case 2:
		if col < size-1 {
			return index + 1
		}
	case 4:
		if row < size-1 {
			return index + size
		}
	case 8:
		if col > 0 {
			return index - 1
		}
	}
	return -1
}
func Won(tiles []int, size int) bool {
	if !ValidSize(size) || len(tiles) != size*size {
		return false
	}
	seen := make([]bool, len(tiles))
	index, incoming := size/2*size, 8
	for !seen[index] && tiles[index]&incoming != 0 {
		seen[index] = true
		outgoing := 0
		for _, p := range ports {
			if p != incoming && tiles[index]&p != 0 {
				outgoing = p
				break
			}
		}
		if outgoing == 0 {
			return false
		}
		if index == size/2*size+size-1 && outgoing == 2 {
			return true
		}
		index = neighbor(index, outgoing, size)
		if index < 0 {
			return false
		}
		incoming = ((outgoing << 2) | (outgoing >> 2)) & 15
	}
	return false
}
func Generate(seed uint32, size int) Circuit {
	if !ValidSize(size) {
		return Circuit{}
	}
	state := seed
	random := func() float64 { state = state*1664525 + 1013904223; return float64(state) / 4294967296 }
	shuffled := func() []int {
		result := append([]int(nil), ports...)
		for i := 3; i > 0; i-- {
			j := int(random() * float64(i+1))
			result[i], result[j] = result[j], result[i]
		}
		return result
	}
	type frame struct {
		index int
		ports []int
		next  int
	}
	start, finish := size/2*size, size/2*size+size-1
	visited := make([]bool, size*size)
	visited[start] = true
	stack := []frame{{start, shuffled(), 0}}
	var route []int
	for len(stack) > 0 {
		current := &stack[len(stack)-1]
		if current.index == finish {
			for _, item := range stack {
				route = append(route, item.index)
			}
			break
		}
		if current.next == 4 {
			stack = stack[:len(stack)-1]
			continue
		}
		next := neighbor(current.index, current.ports[current.next], size)
		current.next++
		if next < 0 || visited[next] || (next == finish && len(stack) < 2*size-2) {
			continue
		}
		visited[next] = true
		stack = append(stack, frame{next, shuffled(), 0})
	}
	if len(route) == 0 {
		for row := size / 2; row >= 0; row-- {
			route = append(route, row*size)
		}
		for col := 1; col < size; col++ {
			route = append(route, col)
		}
		for row := 1; row <= size/2; row++ {
			route = append(route, row*size+size-1)
		}
	}
	solution := make([]int, size*size)
	for i := range solution {
		solution[i] = shapes[int(random()*6)]
	}
	for i, index := range route {
		before, after := 8, 2
		if i > 0 {
			for _, p := range ports {
				if neighbor(index, p, size) == route[i-1] {
					before = p
					break
				}
			}
		}
		if i < len(route)-1 {
			for _, p := range ports {
				if neighbor(index, p, size) == route[i+1] {
					after = p
					break
				}
			}
		}
		solution[index] = before | after
	}
	tiles := append([]int(nil), solution...)
	for i, mask := range tiles {
		turns := int(random() * 4)
		for j := 0; j < turns; j++ {
			mask = Rotate(mask)
		}
		tiles[i] = mask
	}
	for Won(tiles, size) {
		tiles[start] = Rotate(tiles[start])
	}
	return Circuit{tiles, solution, route}
}
func DailySeed(day string, size int) (uint32, error) {
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil || parsed.Format("2006-01-02") != day || !ValidSize(size) {
		return 0, errors.New("invalid challenge")
	}
	digest := sha256.Sum256([]byte("lmm-signal-v2:" + day))
	return binary.BigEndian.Uint32(digest[:4]), nil
}
func Replay(seed uint32, size int, actions []int, allowHints bool) (int, error) {
	if !ValidSize(size) || len(actions) == 0 || len(actions) > MaxMoves(size) {
		return 0, errors.New("invalid move count")
	}
	c := Generate(seed, size)
	tiles := append([]int(nil), c.Tiles...)
	hints := 0
	for _, action := range actions {
		if Won(tiles, size) {
			return 0, errors.New("moves after completion")
		}
		index := action
		if action == -1 && allowHints {
			index = -1
			for _, tile := range c.Route {
				if tiles[tile] != c.Solution[tile] {
					index = tile
					break
				}
			}
			hints++
		}
		if index < 0 || index >= size*size {
			return 0, errors.New("invalid tile or forbidden hint")
		}
		tiles[index] = Rotate(tiles[index])
	}
	if !Won(tiles, size) {
		return 0, errors.New("circuit is not connected")
	}
	return hints, nil
}
