package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaychannel "github.com/LIghtJUNction/api.lmm.best/relay/channel"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
)

func applyAdvancedCustomBalanceHeaderOverrides(channel *model.Channel, key string, header http.Header) error {
	var overrides map[string]interface{}
	if channel.HeaderOverride != nil && *channel.HeaderOverride != "" {
		if err := common.UnmarshalJsonStr(*channel.HeaderOverride, &overrides); err != nil {
			return errors.New("invalid balance request headers")
		}
	}
	info := &relaycommon.RelayInfo{
		IsChannelTest: true,
		ChannelMeta:   &relaycommon.ChannelMeta{ApiKey: key, HeadersOverride: overrides},
	}
	resolved, err := relaychannel.ResolveHeaderOverride(info, nil)
	if err != nil {
		return errors.New("invalid balance request headers")
	}
	for name, value := range resolved {
		header.Set(name, value)
	}
	return nil
}

func validateAdvancedCustomBalanceDestination(target string, header http.Header) error {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("invalid balance endpoint")
	}
	if host := strings.TrimSpace(header.Get("Host")); host != "" && !strings.EqualFold(host, parsed.Host) {
		return errors.New("balance Host override must match the configured endpoint")
	}
	return nil
}

func extractAdvancedCustomBalance(document json.RawMessage, config *dto.AdvancedCustomBalanceConfig) (float64, error) {
	if config == nil || config.JSONPointer == "" {
		return 0, errors.New("invalid balance extraction configuration")
	}
	if err := config.Validate(); err != nil {
		return 0, errors.New("invalid balance extraction configuration")
	}
	tokens, err := dto.AdvancedCustomBalancePointerTokens(config.JSONPointer)
	if err != nil || len(tokens) == 0 {
		return 0, errors.New("invalid balance extraction configuration")
	}
	current := document
	for _, token := range tokens {
		current, err = balancePointerMember(current, token)
		if err != nil {
			return 0, err
		}
	}
	current = bytes.TrimSpace(current)
	if len(current) == 0 || (current[0] != '-' && (current[0] < '0' || current[0] > '9')) {
		return 0, errors.New("balance JSON pointer must select a number")
	}
	var value float64
	if err := json.Unmarshal(current, &value); err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("balance must be finite and non-negative")
	}
	if config.Scale != nil {
		value *= *config.Scale
	}
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("scaled balance must be finite and non-negative")
	}
	return value, nil
}

func balancePointerMember(document json.RawMessage, member string) (json.RawMessage, error) {
	document = bytes.TrimSpace(document)
	if len(document) == 0 {
		return nil, errors.New("balance JSON pointer did not resolve")
	}
	switch document[0] {
	case '{':
		decoder := json.NewDecoder(bytes.NewReader(document))
		if _, err := decoder.Token(); err != nil {
			return nil, errors.New("invalid balance JSON response")
		}
		var selected json.RawMessage
		matches := 0
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, errors.New("invalid balance JSON response")
			}
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return nil, errors.New("invalid balance JSON response")
			}
			if key == member {
				selected = value
				matches++
			}
		}
		if matches != 1 {
			return nil, errors.New("balance JSON pointer did not resolve to a unique value")
		}
		return selected, nil
	case '[':
		if member == "" || (len(member) > 1 && member[0] == '0') {
			return nil, errors.New("balance JSON pointer has an invalid array index")
		}
		for _, digit := range member {
			if digit < '0' || digit > '9' {
				return nil, errors.New("balance JSON pointer has an invalid array index")
			}
		}
		index, err := strconv.ParseUint(member, 10, 64)
		if err != nil {
			return nil, errors.New("balance JSON pointer has an invalid array index")
		}
		var values []json.RawMessage
		if err := json.Unmarshal(document, &values); err != nil {
			return nil, errors.New("invalid balance JSON response")
		}
		if index >= uint64(len(values)) {
			return nil, errors.New("balance JSON pointer did not resolve")
		}
		return values[index], nil
	default:
		return nil, errors.New("balance JSON pointer did not resolve")
	}
}
