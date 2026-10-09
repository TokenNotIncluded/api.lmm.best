/*
Copyright (C) 2023-2026 QuantumNous
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package model

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
)

// ChannelCreationInput is shared by the administrator form and reviewed
// marketplace submissions. Preparation never writes to the database.
type ChannelCreationInput struct {
	// Mode must be single, batch, or multi_to_single.
	Mode                      string                `json:"mode"`
	MultiKeyMode              constant.MultiKeyMode `json:"multi_key_mode"`
	BatchAddSetKeyPrefix2Name bool                  `json:"batch_add_set_key_prefix_2_name"`
	Channel                   *Channel              `json:"channel"`
}

func ValidateChannel(channel *Channel, isAdd bool) error {
	if channel == nil {
		return fmt.Errorf("channel cannot be empty")
	}

	if isAdd && channel.Type == constant.ChannelTypeOpenHuman {
		return ErrRetiredChannelType
	}

	// 校验 channel settings
	if err := channel.ValidateSettings(); err != nil {
		return fmt.Errorf("渠道额外设置[channel setting] 格式错误：%s", err.Error())
	}

	if channel.Type == constant.ChannelTypeNewAPI && strings.TrimSpace(channel.GetBaseURL()) == "" {
		return fmt.Errorf("compatible relay channel base URL cannot be empty")
	}

	// 如果是添加操作，检查 channel 和 key 是否为空
	if isAdd {
		if channel.Key == "" {
			return fmt.Errorf("channel cannot be empty")
		}

		// 检查模型名称长度是否超过 255
		for _, m := range channel.GetModels() {
			if len(m) > 255 {
				return fmt.Errorf("模型名称过长: %s", m)
			}
		}
	}

	// VertexAI 特殊校验
	if channel.Type == constant.ChannelTypeVertexAi {
		if channel.Other == "" {
			return fmt.Errorf("部署地区不能为空")
		}

		regionMap, err := common.StrToMap(channel.Other)
		if err != nil {
			return fmt.Errorf("部署地区必须是标准的Json格式，例如{\"default\": \"us-central1\", \"region2\": \"us-east1\"}")
		}

		if regionMap["default"] == nil {
			return fmt.Errorf("部署地区必须包含default字段")
		}
	}

	// Codex OAuth key validation (optional, only when JSON object is provided)
	if channel.Type == constant.ChannelTypeCodex {
		trimmedKey := strings.TrimSpace(channel.Key)
		if isAdd || trimmedKey != "" {
			if !strings.HasPrefix(trimmedKey, "{") {
				return fmt.Errorf("Codex key must be a valid JSON object")
			}
			var keyMap map[string]any
			if err := common.Unmarshal([]byte(trimmedKey), &keyMap); err != nil {
				return fmt.Errorf("Codex key must be a valid JSON object")
			}
			if v, ok := keyMap["access_token"]; !ok || v == nil || strings.TrimSpace(fmt.Sprintf("%v", v)) == "" {
				return fmt.Errorf("Codex key JSON must include access_token")
			}
			if v, ok := keyMap["account_id"]; !ok || v == nil || strings.TrimSpace(fmt.Sprintf("%v", v)) == "" {
				return fmt.Errorf("Codex key JSON must include account_id")
			}
		}
	}

	return nil
}

func ParseVertexArrayKeys(keys string) ([]string, error) {
	if keys == "" {
		return nil, nil
	}
	var keyArray []interface{}
	err := common.Unmarshal([]byte(keys), &keyArray)
	if err != nil {
		return nil, fmt.Errorf("批量添加 Vertex AI 必须使用标准的JsonArray格式，例如[{key1}, {key2}...]，请检查输入: %w", err)
	}
	cleanKeys := make([]string, 0, len(keyArray))
	for _, key := range keyArray {
		var keyStr string
		switch v := key.(type) {
		case string:
			keyStr = strings.TrimSpace(v)
		default:
			bytes, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("Vertex AI key JSON 编码失败: %w", err)
			}
			keyStr = string(bytes)
		}
		if keyStr != "" {
			cleanKeys = append(cleanKeys, keyStr)
		}
	}
	if len(cleanKeys) == 0 {
		return nil, fmt.Errorf("批量添加 Vertex AI 的 keys 不能为空")
	}
	return cleanKeys, nil
}

// PrepareChannelCreation validates credentials and constructs independent rows.
// Callers choose the transaction so approval, channels, and abilities commit once.
func PrepareChannelCreation(input ChannelCreationInput) ([]Channel, error) {
	if err := ValidateChannel(input.Channel, true); err != nil {
		return nil, err
	}
	template := *input.Channel
	template.CreatedTime = common.GetTimestamp()
	var keys []string
	vertexJSON := template.Type == constant.ChannelTypeVertexAi && template.GetOtherSettings().VertexKeyType != dto.VertexKeyTypeAPIKey
	switch input.Mode {
	case "single":
		keys = []string{strings.TrimSpace(template.Key)}
	case "batch", "multi_to_single":
		if vertexJSON {
			var err error
			keys, err = ParseVertexArrayKeys(template.Key)
			if err != nil {
				return nil, err
			}
		} else {
			for _, key := range strings.Split(template.Key, "\n") {
				if key = strings.TrimSpace(key); key != "" {
					keys = append(keys, key)
				}
			}
		}
		if input.Mode == "multi_to_single" {
			mode := input.MultiKeyMode
			if mode == "" {
				mode = "random"
			}
			if mode != "random" && mode != "polling" {
				return nil, fmt.Errorf("invalid multi-key mode")
			}
			template.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeyMode: mode, MultiKeySize: len(keys)}
			keys = []string{strings.Join(keys, "\n")}
		}
	default:
		return nil, fmt.Errorf("unsupported channel creation mode")
	}
	channels := make([]Channel, 0, len(keys))
	for _, key := range keys {
		if strings.TrimSpace(key) == "" {
			continue
		}
		channel := template
		channel.Key = key
		if input.BatchAddSetKeyPrefix2Name && len(keys) > 1 {
			prefix := key
			if len(prefix) > 8 {
				prefix = prefix[:8]
			}
			channel.Name = fmt.Sprintf("%s %s", template.Name, prefix)
		}
		channels = append(channels, channel)
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("channel credentials cannot be empty")
	}
	return channels, nil
}
