// Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later
package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

type assistantVisualSeries struct {
	Name   string    `json:"name"`
	Values []float64 `json:"values"`
}
type assistantVisualItem struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Detail string `json:"detail,omitempty"`
	Icon   string `json:"icon,omitempty"`
}
type assistantVisualNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type assistantVisualEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
}

// Data only: there is deliberately no HTML, script, remote image, URL or
// executable chart configuration in this protocol.
type assistantVisualization struct {
	Kind      string                  `json:"kind"`
	Title     string                  `json:"title"`
	Source    string                  `json:"source,omitempty"`
	ChartType string                  `json:"chart_type,omitempty"`
	Unit      string                  `json:"unit,omitempty"`
	Labels    []string                `json:"labels,omitempty"`
	Series    []assistantVisualSeries `json:"series,omitempty"`
	Items     []assistantVisualItem   `json:"items,omitempty"`
	Nodes     []assistantVisualNode   `json:"nodes,omitempty"`
	Edges     []assistantVisualEdge   `json:"edges,omitempty"`
}

func assistantVisualizationKind(name string) string {
	switch name {
	case "show_chart":
		return "chart"
	case "show_statistics":
		return "statistics"
	case "show_choices":
		return "choices"
	case "show_flowchart":
		return "flowchart"
	}
	return ""
}
func assistantVisualText(text string, max int, required bool) bool {
	return utf8.ValidString(text) && utf8.RuneCountInString(text) <= max && (!required || strings.TrimSpace(text) != "") && !strings.ContainsRune(text, 0)
}

func parseAssistantVisualization(name string, input map[string]any) (*assistantVisualization, error) {
	invalid := errors.New("invalid visualization data; use bounded labels and finite values, never HTML or executable code")
	kind := assistantVisualizationKind(name)
	if kind == "" {
		return nil, invalid
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > assistantToolArgumentsMaxBytes {
		return nil, invalid
	}
	var visual assistantVisualization
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&visual); err != nil {
		return nil, invalid
	}
	if visual.Kind != "" && visual.Kind != kind {
		return nil, invalid
	}
	visual.Kind = kind
	if !assistantVisualText(visual.Title, 120, true) || !assistantVisualText(visual.Source, 300, false) || !assistantVisualText(visual.Unit, 30, false) {
		return nil, invalid
	}
	if kind != "chart" && (visual.ChartType != "" || len(visual.Labels) > 0 || len(visual.Series) > 0 || visual.Unit != "") {
		return nil, invalid
	}
	if kind != "flowchart" && (len(visual.Nodes) > 0 || len(visual.Edges) > 0) {
		return nil, invalid
	}
	if kind != "statistics" && kind != "choices" && len(visual.Items) > 0 {
		return nil, invalid
	}
	switch kind {
	case "chart":
		if visual.ChartType != "line" && visual.ChartType != "bar" && visual.ChartType != "donut" {
			return nil, invalid
		}
		if len(visual.Labels) < 1 || len(visual.Labels) > 80 || len(visual.Series) < 1 || len(visual.Series) > 4 || (visual.ChartType == "donut" && len(visual.Series) != 1) {
			return nil, invalid
		}
		for _, label := range visual.Labels {
			if !assistantVisualText(label, 80, true) {
				return nil, invalid
			}
		}
		for _, series := range visual.Series {
			if !assistantVisualText(series.Name, 80, true) || len(series.Values) != len(visual.Labels) {
				return nil, invalid
			}
			for _, value := range series.Values {
				if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 1e15 || (visual.ChartType == "donut" && value < 0) {
					return nil, invalid
				}
			}
		}
	case "statistics", "choices":
		if len(visual.Items) < 1 || len(visual.Items) > 8 {
			return nil, invalid
		}
		for _, item := range visual.Items {
			limit := 100
			if kind == "choices" {
				limit = 500
			}
			if !assistantVisualText(item.Label, 80, true) || !assistantVisualText(item.Value, limit, true) || !assistantVisualText(item.Detail, 180, false) {
				return nil, invalid
			}
			switch item.Icon {
			case "", "activity", "wallet", "clock", "check", "sparkles", "chart":
			default:
				return nil, invalid
			}
		}
	case "flowchart":
		if len(visual.Nodes) < 1 || len(visual.Nodes) > 16 || len(visual.Edges) > 24 {
			return nil, invalid
		}
		ids := map[string]bool{}
		for _, node := range visual.Nodes {
			if !assistantVisualText(node.ID, 40, true) || ids[node.ID] || !assistantVisualText(node.Label, 80, true) {
				return nil, invalid
			}
			ids[node.ID] = true
		}
		for _, edge := range visual.Edges {
			if !ids[edge.From] || !ids[edge.To] || edge.From == edge.To || !assistantVisualText(edge.Label, 80, false) {
				return nil, invalid
			}
		}
	}
	return &visual, nil
}

func assistantVisualizationToolDefinitions() []assistantOpenAIToolDefinition {
	text := func(max int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
	}
	array := func(items any, max int) map[string]any {
		return map[string]any{"type": "array", "items": items, "minItems": 1, "maxItems": max}
	}
	definitions := []assistantOpenAIToolDefinition{}
	for _, name := range []string{"show_chart", "show_statistics", "show_choices", "show_flowchart"} {
		properties := map[string]any{"title": text(120), "source": text(300)}
		required := []string{"title"}
		switch name {
		case "show_chart":
			properties["chart_type"] = map[string]any{"type": "string", "enum": []string{"line", "bar", "donut"}}
			properties["unit"] = text(30)
			properties["labels"] = array(text(80), 80)
			properties["series"] = array(objectSchema(map[string]any{"name": text(80), "values": array(map[string]any{"type": "number", "minimum": -1e15, "maximum": 1e15}, 80)}, []string{"name", "values"}), 4)
			required = append(required, "chart_type", "labels", "series")
		case "show_statistics", "show_choices":
			properties["items"] = array(objectSchema(map[string]any{"label": text(80), "value": text(500), "detail": text(180), "icon": map[string]any{"type": "string", "enum": []string{"activity", "wallet", "clock", "check", "sparkles", "chart"}}}, []string{"label", "value"}), 8)
			required = append(required, "items")
		case "show_flowchart":
			properties["nodes"] = array(objectSchema(map[string]any{"id": text(40), "label": text(80)}, []string{"id", "label"}), 16)
			properties["edges"] = map[string]any{"type": "array", "maxItems": 24, "items": objectSchema(map[string]any{"from": text(40), "to": text(40), "label": text(80)}, []string{"from", "to"})}
			required = append(required, "nodes", "edges")
		}
		definitions = append(definitions, assistantOpenAIToolDefinition{Type: "function", Function: assistantOpenAIToolFunction{Name: name, Description: "Display an accessible interactive visualization in the current chat. Use verified tool results for account statistics and state the data source. Never invent account data. Choice buttons only fill the user's composer; they never authorize writes or payments. Supply data only, without HTML, scripts, URLs or image generation.", Parameters: objectSchema(properties, required)}})
	}
	return definitions
}
