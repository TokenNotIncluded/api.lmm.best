package model

import (
	"encoding/xml"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Animation has a separate attribute contract: a general SVG attribute list
// would allow SMIL to write href/style or to start in response to user events.
var storeSVGAnimationAttrs = storeSVGSet("attributeName attributeType from to by values dur begin repeatCount fill calcMode keyTimes keySplines additive accumulate type")
var storeSVGAnimationParents = storeSVGSet("svg g path rect circle ellipse line polyline polygon text tspan stop")
var storeSVGAnimationOpacity = storeSVGSet("opacity fill-opacity stroke-opacity stop-opacity")
var storeSVGAnimationColor = storeSVGSet("fill stroke color stop-color")
var storeSVGAnimationClock = regexp.MustCompile(`^(?:\d+\.?\d*|\.\d+)(?:ms|s|min|h)?$`)
var storeSVGAnimationColorValue = regexp.MustCompile(`^(?:#[a-fA-F0-9]{3,4}|#[a-fA-F0-9]{6}|#[a-fA-F0-9]{8}|[a-zA-Z]{1,32})$`)

func storeSVGAnimationTag(name string) bool {
	return name == "animate" || name == "animateTransform"
}

func storeSVGAnimationNumber(value string, min, max float64) bool {
	if !storeSVGNumber.MatchString(value) {
		return false
	}
	number, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) && number >= min && number <= max
}

func storeSVGAnimationTime(value string, positive bool) bool {
	if !storeSVGAnimationClock.MatchString(value) {
		return false
	}
	for _, unit := range []string{"min", "ms", "s", "h"} {
		value = strings.TrimSuffix(value, unit)
	}
	number, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsInf(number, 0) && number >= 0 && (!positive || number > 0)
}

func storeSVGAnimationTransform(value, kind string) bool {
	parts := strings.Fields(strings.ReplaceAll(value, ",", " "))
	length := len(parts)
	if !(kind == "translate" && (length == 1 || length == 2) || kind == "scale" && (length == 1 || length == 2) || kind == "rotate" && (length == 1 || length == 3) || (kind == "skewX" || kind == "skewY") && length == 1) {
		return false
	}
	for _, part := range parts {
		if !storeSVGAnimationNumber(part, -4096, 4096) {
			return false
		}
	}
	return true
}

func storeSVGAnimationSafe(token xml.StartElement, parent string) bool {
	if !storeSVGAnimationParents[parent] {
		return false
	}
	attrs := map[string]string{}
	for _, attr := range token.Attr {
		if !storeSVGAnimationAttrs[attr.Name.Local] || strings.TrimSpace(attr.Value) != attr.Value {
			return false
		}
		attrs[attr.Name.Local] = attr.Value
	}
	if !storeSVGAnimationTime(attrs["dur"], true) || attrs["attributeType"] != "" && attrs["attributeType"] != "XML" {
		return false
	}
	if value, exists := attrs["begin"]; exists && !storeSVGAnimationTime(value, false) {
		return false
	}
	if value, exists := attrs["repeatCount"]; exists && value != "indefinite" && !storeSVGAnimationNumber(value, math.SmallestNonzeroFloat64, math.MaxFloat64) {
		return false
	}
	for name, allowed := range map[string]map[string]bool{
		"fill":       storeSVGSet("remove freeze"),
		"calcMode":   storeSVGSet("linear discrete paced spline"),
		"additive":   storeSVGSet("replace sum"),
		"accumulate": storeSVGSet("none sum"),
	} {
		if value, exists := attrs[name]; exists && !allowed[value] {
			return false
		}
	}
	var validValue func(string) bool
	if token.Name.Local == "animateTransform" {
		if attrs["attributeName"] != "transform" || parent == "stop" || !storeSVGSet("translate scale rotate skewX skewY")[attrs["type"]] {
			return false
		}
		validValue = func(value string) bool { return storeSVGAnimationTransform(value, attrs["type"]) }
	} else {
		name := attrs["attributeName"]
		if _, exists := attrs["type"]; exists || strings.HasPrefix(name, "stop-") && parent != "stop" {
			return false
		}
		switch {
		case storeSVGAnimationOpacity[name]:
			validValue = func(value string) bool { return storeSVGAnimationNumber(value, 0, 1) }
		case name == "stroke-dashoffset":
			validValue = func(value string) bool { return storeSVGAnimationNumber(value, -4096, 4096) }
		case storeSVGAnimationColor[name]:
			validValue = storeSVGAnimationColorValue.MatchString
		default:
			return false
		}
	}
	frames := 2
	if values, exists := attrs["values"]; exists {
		if _, exists := attrs["from"]; exists {
			return false
		}
		if _, exists := attrs["to"]; exists {
			return false
		}
		if _, exists := attrs["by"]; exists {
			return false
		}
		parts := strings.Split(values, ";")
		frames = len(parts)
		if frames < 2 || frames > 256 {
			return false
		}
		for _, value := range parts {
			if !validValue(strings.TrimSpace(value)) {
				return false
			}
		}
	} else {
		_, to := attrs["to"]
		_, by := attrs["by"]
		if to == by {
			return false
		}
		for _, field := range []string{"from", "to", "by"} {
			if value, exists := attrs[field]; exists && !validValue(value) {
				return false
			}
		}
	}
	if value, exists := attrs["keyTimes"]; exists {
		parts := strings.Split(value, ";")
		if len(parts) != frames {
			return false
		}
		previous := 0.0
		for index, part := range parts {
			part = strings.TrimSpace(part)
			if !storeSVGAnimationNumber(part, previous, 1) {
				return false
			}
			current, _ := strconv.ParseFloat(part, 64)
			if index == 0 && current != 0 || index == frames-1 && current != 1 {
				return false
			}
			previous = current
		}
	}
	if value, exists := attrs["keySplines"]; exists {
		parts := strings.Split(value, ";")
		if attrs["calcMode"] != "spline" || len(parts) != frames-1 {
			return false
		}
		for _, part := range parts {
			coordinates := strings.Fields(strings.ReplaceAll(part, ",", " "))
			if len(coordinates) != 4 {
				return false
			}
			for _, coordinate := range coordinates {
				if !storeSVGAnimationNumber(coordinate, 0, 1) {
					return false
				}
			}
		}
	} else if attrs["calcMode"] == "spline" {
		return false
	}
	return true
}
